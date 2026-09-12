using System.Text.RegularExpressions;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class ProductionDeployOrchestrationTests
{
    private static string Script => File.ReadAllText(Path.Combine(
        SqlServerAvailability.LicensingRoot, "scripts", "production-deploy.ps1"));

    [Fact]
    public void TestProductionDeployStagesAppearInRequiredOrder()
    {
        var required = new[]
        {
            "precheck",
            "package_validation",
            "backup_database",
            "migration_rehearsal",
            "backup_binaries_config",
            "stop_service",
            "apply_production_migration",
            "validate_production_schema",
            "deploy_binaries",
            "start_service",
            "production_gate"
        };
        var assignments = Regex.Matches(Script, @"(?im)^\s*\$stage\s*=\s*'(?<stage>[^']+)'")
            .Select(match => match.Groups["stage"].Value)
            .ToArray();

        var cursor = -1;
        foreach (var stage in required)
        {
            var next = Array.FindIndex(assignments, cursor + 1,
                candidate => string.Equals(candidate, stage, StringComparison.Ordinal));
            Assert.True(next > cursor, $"Required deployment stage '{stage}' is missing or out of order.");
            cursor = next;
        }
    }

    [Fact]
    public void TestRollbackContractReportsEveryComponent()
    {
        foreach (var component in new[] { "binaries", "config", "database", "service", "health" })
            Assert.Contains($"{component}=$($rollback", Script, StringComparison.Ordinal);
        Assert.Contains("rollback_complete=$($rollbackComplete", Script, StringComparison.Ordinal);
        Assert.Contains("$rollbackComplete = $rollbackBinaries -and $rollbackConfig -and $rollbackDatabase",
            Script, StringComparison.Ordinal);
    }

    [Fact]
    public void TestMigrationRehearsalOccursBeforeStoppingOnly()
    {
        var rehearsal = Script.IndexOf("$stage = 'migration_rehearsal'", StringComparison.OrdinalIgnoreCase);
        var deployStarted = Script.IndexOf("$deployStarted = $true", StringComparison.OrdinalIgnoreCase);
        var stopping = Script.IndexOf("before InstallDir mutation", StringComparison.OrdinalIgnoreCase);

        Assert.True(rehearsal >= 0 && deployStarted > rehearsal && stopping > deployStarted,
            "Migration rehearsal must complete before the deployment mutates or stops the service.");
        Assert.Contains("Stop-NyxveilWindowsServiceFully", Script, StringComparison.Ordinal);
        Assert.Contains("Assert-NyxveilInstallDirUnlockedForMutation", Script, StringComparison.Ordinal);
        // Must stop updater before Clear-DirectoryContents on InstallDir.
        var unlockIdx = Script.IndexOf("Assert-NyxveilInstallDirUnlockedForMutation", StringComparison.Ordinal);
        var clearIdx = Script.IndexOf("Clear-DirectoryContents -Path $InstallDir", StringComparison.Ordinal);
        Assert.True(unlockIdx > 0 && clearIdx > unlockIdx,
            "InstallDir unlock assert must precede Clear-DirectoryContents.");
        Assert.All(Regex.Matches(Script, @"(?im)^\s*Stop-Service\b").Cast<Match>(),
            match => Assert.True(match.Index > rehearsal,
                "No service stop is allowed before migration rehearsal."));
    }

    [Fact]
    public void TestRollbackStopsUpdaterBeforeBinaryRestore()
    {
        var catchIdx = Script.IndexOf("if ($deployStarted)", StringComparison.Ordinal);
        Assert.True(catchIdx > 0);
        var rollback = Script.Substring(catchIdx);
        var stopUpdater = rollback.IndexOf("Stop-NyxveilWindowsServiceFully -ServiceName $updaterServiceName", StringComparison.Ordinal);
        var clear = rollback.IndexOf("Clear-DirectoryContents -Path $InstallDir", StringComparison.Ordinal);
        Assert.True(stopUpdater >= 0 && clear > stopUpdater,
            "Rollback must stop updater before clearing InstallDir for binary restore.");
        Assert.Contains("Start-NyxveilWindowsServiceIfWasRunning -Snapshot $updaterSnapshotBefore", rollback, StringComparison.Ordinal);
        Assert.Contains("Start-NyxveilWindowsServiceIfWasRunning -Snapshot $webSnapshotBefore", rollback, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRepairDeployAllowsEmptyInstallDirBackup()
    {
        Assert.Contains("Created missing InstallDir for repair deploy", Script, StringComparison.Ordinal);
        Assert.Contains("binary backup is empty (repair deploy)", Script, StringComparison.Ordinal);
        // Precheck must not require healthy Web — only that the service exists.
        var precheck = Script.IndexOf("# 1. PRECHECK:", StringComparison.Ordinal);
        var packageValidation = Script.IndexOf("$stage = 'package_validation'", StringComparison.Ordinal);
        Assert.True(precheck >= 0 && packageValidation > precheck);
        var precheckBody = Script.Substring(precheck, packageValidation - precheck);
        Assert.DoesNotContain("Wait-HttpsHealthy", precheckBody, StringComparison.Ordinal);
        Assert.DoesNotContain("health/live", precheckBody, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Get-Service -Name $requiredServiceName -ErrorAction Stop", precheckBody, StringComparison.Ordinal);
    }

    [Fact]
    public void TestSchemaV5NoOpSkipsProductionMigrationFlag()
    {
        // When already at expected schema, productionMigrationAttempted must stay false
        // so rollback does not restore an untouched database.
        Assert.Contains("if ($productionPlan.MigrationRequired)", Script, StringComparison.Ordinal);
        Assert.Contains("$productionMigrationAttempted = $true", Script, StringComparison.Ordinal);
        var applyIdx = Script.IndexOf("$stage = 'apply_production_migration'", StringComparison.Ordinal);
        var flagIdx = Script.IndexOf("$productionMigrationAttempted = $true", applyIdx, StringComparison.Ordinal);
        Assert.True(flagIdx > applyIdx);
        var flagContext = Script.Substring(Math.Max(0, flagIdx - 120), Math.Min(200, Script.Length - Math.Max(0, flagIdx - 120)));
        Assert.Contains("MigrationRequired", flagContext, StringComparison.Ordinal);
    }

    [Fact]
    public void TestMigrationRehearsalUsesValidateSchemaV5AndDetectsSchema()
    {
        Assert.Contains("Get-SchemaVersionFromDatabase", Script, StringComparison.Ordinal);
        Assert.Contains("validate_schema_v5.sql", Script, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Resolve-SchemaMigrationPlan", Script, StringComparison.Ordinal);
        Assert.Contains("CurrentSchemaVersion -lt 5", Script, StringComparison.Ordinal);
        Assert.DoesNotContain("Invoke-MigrationRehearsal -BackupPath $databaseBackup -MigrationPath",
            Script, StringComparison.Ordinal);
    }
}
