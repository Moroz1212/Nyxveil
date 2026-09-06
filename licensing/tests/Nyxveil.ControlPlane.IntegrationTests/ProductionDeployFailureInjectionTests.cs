using System.Text.RegularExpressions;

namespace Nyxveil.ControlPlane.IntegrationTests;

/// <summary>
/// Contract tests for controlled failure injection points in production-deploy.ps1.
/// Full service orchestration requires a Windows service host; these tests lock the
/// expected gate names and rollback semantics for every injectable stage.
/// </summary>
public sealed class ProductionDeployFailureInjectionTests
{
    private static string Script => File.ReadAllText(Path.Combine(
        SqlServerAvailability.LicensingRoot, "scripts", "production-deploy.ps1"));

    public static TheoryData<string> InjectableFailureGates => new()
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

    [Theory]
    [MemberData(nameof(InjectableFailureGates))]
    public void TestFailureGateIsRecognizedStage(string gate)
    {
        Assert.Contains($"$stage = '{gate}'", Script, StringComparison.Ordinal);
    }

    [Fact]
    public void TestMigrationRehearsalFailureDoesNotStartDeploy()
    {
        var rehearsal = Script.IndexOf("$stage = 'migration_rehearsal'", StringComparison.Ordinal);
        var deployStarted = Script.IndexOf("$deployStarted = $true", StringComparison.Ordinal);
        Assert.True(rehearsal >= 0 && deployStarted > rehearsal);
        Assert.Contains("$failedGate = 'migration_rehearsal'", Script, StringComparison.Ordinal);
        // Before deployStarted, catch path must not enter binary/service rollback mutation.
        var catchBlock = Script[Script.IndexOf("catch {", StringComparison.Ordinal)..];
        Assert.Contains("if ($deployStarted)", catchBlock, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRealMigrationFailureRequiresDatabaseRestoreForCompleteRollback()
    {
        Assert.Contains("$productionMigrationAttempted = $true", Script, StringComparison.Ordinal);
        Assert.Contains("$rollbackDatabase = (-not $productionMigrationAttempted)", Script, StringComparison.Ordinal);
        Assert.Contains("restore-db.ps1", Script, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("AutomatedRollback", Script, StringComparison.OrdinalIgnoreCase);
        // Complete rollback requires every component including database.
        Assert.Matches(new Regex(
            @"\$rollbackComplete\s*=\s*\$rollbackBinaries\s*-and\s*\$rollbackConfig\s*-and\s*\$rollbackDatabase\s*-and\s*\$rollbackService\s*-and\s*\$rollbackHealth",
            RegexOptions.IgnoreCase), Script);
    }

    [Fact]
    public void TestDiagnosticBundleOmitsSecrets()
    {
        Assert.Contains("ConvertTo-SanitizedText", Script, StringComparison.Ordinal);
        Assert.Contains("New-SanitizedDiagnosticBundle", Script, StringComparison.Ordinal);
        Assert.DoesNotContain("SQLCMDPASSWORD=", Script, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("<redacted>", Script, StringComparison.OrdinalIgnoreCase);
    }
}
