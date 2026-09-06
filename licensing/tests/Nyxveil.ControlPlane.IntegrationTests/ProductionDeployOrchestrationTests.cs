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
        var stopping = Script.IndexOf("Stopping only $requiredServiceName", StringComparison.OrdinalIgnoreCase);

        Assert.True(rehearsal >= 0 && deployStarted > rehearsal && stopping > deployStarted,
            "Migration rehearsal must complete before the deployment mutates or stops the service.");
        Assert.All(Regex.Matches(Script, @"(?im)^\s*Stop-Service\b").Cast<Match>(),
            match => Assert.True(match.Index > rehearsal,
                "No service stop is allowed before migration rehearsal."));
    }
}
