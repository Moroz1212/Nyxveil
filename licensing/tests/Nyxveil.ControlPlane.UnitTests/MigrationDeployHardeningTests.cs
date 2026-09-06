using System.Text.RegularExpressions;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Static regressions for the exact live Msg 207 failure mode and restore Msg 3102 class of bugs.
/// Real SQL Server execution tests live in MigrationSqlServerTests (conditional).
/// </summary>
public sealed class MigrationDeployHardeningTests
{
    private static string LicensingRoot
    {
        get
        {
            var dir = new DirectoryInfo(AppContext.BaseDirectory);
            while (dir is not null)
            {
                var candidate = Path.Combine(dir.FullName, "database", "migrations", "002_node_lifecycle_cert_metadata.sql");
                if (File.Exists(candidate))
                    return dir.FullName;
                dir = dir.Parent;
            }
            throw new DirectoryNotFoundException("licensing root with database/migrations not found");
        }
    }

    [Fact]
    public void TestBrokenFixtureStillContainsSameBatchLifecycleReference()
    {
        var broken = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "fixtures", "002_broken_lifecycle_same_batch.sql"));
        Assert.Contains("ALTER TABLE dbo.Nodes ADD LifecycleState", broken, StringComparison.Ordinal);
        // Static reference in same script/batch — the live Msg 207 pattern.
        Assert.Matches(new Regex(
            @"ADD\s+CONSTRAINT\s+CK_Nodes_LifecycleState_Broken\s+CHECK\s*\(\s*LifecycleState",
            RegexOptions.IgnoreCase | RegexOptions.Singleline), broken);
        Assert.DoesNotContain("EXEC(", broken, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestMigration002UsesDynamicSqlForLifecycleDependents()
    {
        var sql = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "002_node_lifecycle_cert_metadata.sql"));

        Assert.Contains("COL_LENGTH(N'dbo.Nodes', N'LifecycleState')", sql, StringComparison.Ordinal);
        Assert.Contains("EXEC(N'ALTER TABLE dbo.Nodes WITH CHECK ADD CONSTRAINT CK_Nodes_LifecycleState", sql, StringComparison.Ordinal);
        Assert.Contains("EXEC(N'CREATE INDEX IX_Nodes_LifecycleState ON dbo.Nodes([LifecycleState])", sql, StringComparison.Ordinal);

        // Must NOT have a static CHECK (LifecycleState ...) outside EXEC — Msg 207 class.
        var withoutExecBodies = Regex.Replace(sql, @"EXEC\s*\(\s*N'[\s\S]*?'\s*\)", "EXEC_PLACEHOLDER", RegexOptions.IgnoreCase);
        Assert.DoesNotMatch(new Regex(
            @"ADD\s+CONSTRAINT\s+CK_Nodes_LifecycleState\s+CHECK\s*\(\s*\[?LifecycleState\]?",
            RegexOptions.IgnoreCase), withoutExecBodies);
        Assert.DoesNotMatch(new Regex(
            @"CREATE\s+INDEX\s+IX_Nodes_LifecycleState\s+ON\s+dbo\.Nodes\s*\(\s*\[?LifecycleState\]?",
            RegexOptions.IgnoreCase), withoutExecBodies);
    }

    [Fact]
    public void TestMigration002SchemaVersionIsLast()
    {
        var sql = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "002_node_lifecycle_cert_metadata.sql"));
        var validationIdx = sql.IndexOf("Migration validation failed", StringComparison.Ordinal);
        var versionIdx = sql.IndexOf("Version = 2", StringComparison.Ordinal);
        Assert.True(validationIdx > 0 && versionIdx > validationIdx,
            "schema version bump must occur after validation");
        Assert.Contains("NyxveilSchemaVersion", sql, StringComparison.Ordinal);
        Assert.Contains("BEGIN TRY", sql, StringComparison.Ordinal);
        Assert.Contains("ROLLBACK TRANSACTION", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRestoreScriptConnectsToMaster()
    {
        var restore = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "restore-db.ps1"));
        Assert.Contains("DatabaseName 'master'", restore, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("USE [master]", restore, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("SET SINGLE_USER WITH ROLLBACK IMMEDIATE", restore, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("SET MULTI_USER", restore, StringComparison.OrdinalIgnoreCase);
        // Must not invoke sql against the target DB for the RESTORE batch.
        Assert.DoesNotMatch(new Regex(
            @"Invoke-NyxveilSql[\s\S]{0,200}-DatabaseName\s+\$Database\b",
            RegexOptions.IgnoreCase), restore);
    }

    [Fact]
    public void TestProductionDeployRequiresMigrationRehearsalBeforeStop()
    {
        var deploy = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "production-deploy.ps1"));
        Assert.Contains("1.1.1", deploy, StringComparison.Ordinal);
        Assert.Contains("migration_rehearsal", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("publish_payload_sha256", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("rollback_complete", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("database=", deploy, StringComparison.OrdinalIgnoreCase);

        var rehearsalIdx = deploy.IndexOf("migration_rehearsal", StringComparison.OrdinalIgnoreCase);
        var stopIdx = deploy.IndexOf("Stopping only", StringComparison.OrdinalIgnoreCase);
        Assert.True(rehearsalIdx > 0 && stopIdx > rehearsalIdx,
            "migration rehearsal must run before stopping the production service");
    }

    [Fact]
    public void TestValidateSchemaV2ScriptExists()
    {
        var path = Path.Combine(LicensingRoot, "database", "migrations", "validate_schema_v2.sql");
        Assert.True(File.Exists(path));
        var sql = File.ReadAllText(path);
        Assert.Contains("schema_version", sql, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("LifecycleState", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestMigration002UsesDynamicSqlForOptionalEfHistory()
    {
        var sql = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "002_node_lifecycle_cert_metadata.sql"));
        Assert.Contains("__EFMigrationsHistory", sql, StringComparison.Ordinal);
        // Static EXISTS against optional history table must not remain outside EXEC (Msg 208).
        var withoutExecBodies = Regex.Replace(sql, @"EXEC\s*\(\s*N'[\s\S]*?'\s*\)", "EXEC_PLACEHOLDER", RegexOptions.IgnoreCase);
        Assert.DoesNotMatch(new Regex(
            @"NOT EXISTS\s*\(\s*SELECT[\s\S]*?FROM\s+dbo\.__EFMigrationsHistory",
            RegexOptions.IgnoreCase), withoutExecBodies);
    }
}
