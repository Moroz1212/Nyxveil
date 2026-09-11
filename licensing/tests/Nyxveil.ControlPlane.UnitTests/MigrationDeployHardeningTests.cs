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
        Assert.Contains("1.3.4", deploy, StringComparison.Ordinal);
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
    public void TestProductionDeployUsesValidateSchemaV5()
    {
        var deploy = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "production-deploy.ps1"));
        Assert.Contains(@"database\migrations\validate_schema_v5.sql", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain(@"$validationScript = Join-Path $licensingRoot 'database\migrations\validate_schema_v3.sql'",
            deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Resolve-SchemaMigrationPlan", deploy, StringComparison.Ordinal);
        Assert.Contains("already_at_or_above_expected", deploy, StringComparison.Ordinal);
        Assert.Contains("005_certificate_operation_states.sql", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("ExpectedSchemaVersion = '5'", deploy, StringComparison.Ordinal);
        Assert.Contains("rehearses schema v5", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("$migration005", deploy, StringComparison.Ordinal);
        Assert.Contains("@($migration002, $migration003, $migration004, $migration005, $validationScript)",
            deploy, StringComparison.Ordinal);
    }

    [Fact]
    public void TestProductionDeployDoesNotDefaultToObsoleteMigration002()
    {
        var deploy = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "production-deploy.ps1"));
        Assert.DoesNotContain(
            "Join-Path $licensingRoot 'database\\migrations\\002_node_lifecycle_cert_metadata.sql'\r\n    }",
            deploy, StringComparison.Ordinal);
        Assert.DoesNotContain(
            "else {\r\n        Join-Path $licensingRoot 'database\\migrations\\002_node_lifecycle_cert_metadata.sql'",
            deploy, StringComparison.Ordinal);
        // Auto chain may reference 002 only for schemas < 2, never as unconditional default apply.
        Assert.Contains("CurrentSchemaVersion -lt 2", deploy, StringComparison.Ordinal);
        Assert.Contains("004_version_mgmt_signing_retiring.sql", deploy, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("migration_required=$($productionPlan.MigrationRequired)", deploy, StringComparison.Ordinal);
        Assert.Contains("skipping migration apply", deploy, StringComparison.OrdinalIgnoreCase);
        // Unconditional default assignment to 002 must not remain.
        Assert.DoesNotMatch(new Regex(
            @"\$MigrationScript\s*=\s*if\s*\(\s*\$MigrationScript\s*\)[\s\S]{0,120}002_node_lifecycle_cert_metadata",
            RegexOptions.IgnoreCase), deploy);
    }

    [Fact]
    public void TestSchemaV5HotfixArtifactsExist()
    {
        Assert.True(File.Exists(Path.Combine(LicensingRoot, "database", "migrations", "validate_schema_v5.sql")));
        Assert.True(File.Exists(Path.Combine(LicensingRoot, "database", "migrations", "005_certificate_operation_states.sql")));
        var validate = File.ReadAllText(Path.Combine(LicensingRoot, "database", "migrations", "validate_schema_v5.sql"));
        Assert.Contains("@v < 5", validate, StringComparison.Ordinal);
        Assert.Contains("[Status]>=0AND[Status]<=9", validate, StringComparison.Ordinal);
    }

    [Fact]
    public void TestSchemaV4ToV5MigrationArtifactExists()
    {
        var sql = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "005_certificate_operation_states.sql"));
        Assert.Contains("Version BETWEEN 4 AND 5", sql, StringComparison.Ordinal);
        Assert.Contains("BETWEEN 0 AND 9", sql, StringComparison.Ordinal);
        Assert.Contains("20260909160000_CertificateOperationStates", sql, StringComparison.Ordinal);
        Assert.Contains("BEGIN TRAN", sql, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("__EFMigrationsHistory", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestValidateSchemaV5ScriptExists()
    {
        var path = Path.Combine(LicensingRoot, "database", "migrations", "validate_schema_v5.sql");
        Assert.True(File.Exists(path));
        var sql = File.ReadAllText(path);
        Assert.Contains("NyxveilSchemaVersion", sql, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("@v < 5", sql, StringComparison.Ordinal);
        Assert.Contains("ReportedServerVersion", sql, StringComparison.Ordinal);
        Assert.Contains("RetireAfter", sql, StringComparison.Ordinal);
        Assert.Contains("ProgressPhase", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestCreateDatabaseSeedsOperationalSchemaVersion5()
    {
        var sql = File.ReadAllText(Path.Combine(LicensingRoot, "database", "create_database.sql"));
        var body = sql.Split("-- END EF GENERATED BASELINE", 2)[1];
        Assert.Contains("NyxveilSchemaVersion", body, StringComparison.Ordinal);
        Assert.Contains("20260909160000_CertificateOperationStates", body, StringComparison.Ordinal);
        Assert.Contains("VALUES (1, 5,", body, StringComparison.Ordinal);
        // Must not alter the EF-generated body used by SchemaAlignmentTests.
        var efBody = sql.Split("-- BEGIN EF GENERATED BASELINE")[1].Split("-- END EF GENERATED BASELINE")[0];
        Assert.DoesNotContain("NyxveilSchemaVersion", efBody, StringComparison.Ordinal);
    }

    [Fact]
    public void TestProductionGateValidatesSchemaV5()
    {
        var gate = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "production-gate.ps1"));
        Assert.Contains(@"validate_schema_v5.sql", gate, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("schema v5 validation could not complete", gate, StringComparison.Ordinal);
        Assert.Contains("005_certificate_operation_states.sql", gate, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain(@"validate_schema_v3.sql') `", gate, StringComparison.Ordinal);
    }

    [Fact]
    public void TestValidateSchemaV4ScriptStillExistsForHistory()
    {
        var path = Path.Combine(LicensingRoot, "database", "migrations", "validate_schema_v4.sql");
        Assert.True(File.Exists(path));
        var sql = File.ReadAllText(path);
        Assert.Contains("NyxveilSchemaVersion", sql, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("@v < 4", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestSchemaV4MigrationArtifactStillExistsForHistory()
    {
        var sql = File.ReadAllText(Path.Combine(
            LicensingRoot, "database", "migrations", "004_version_mgmt_signing_retiring.sql"));
        Assert.Contains("ReportedServerVersion", sql, StringComparison.Ordinal);
        Assert.Contains("Version = 4", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestPackReleaseExcludesTmpAndTrx()
    {
        var pack = File.ReadAllText(Path.Combine(LicensingRoot, "scripts", "pack-release.ps1"));
        Assert.Contains("TestResults", pack, StringComparison.Ordinal);
        Assert.Contains(@"\.(pfx|dpapi|user|trx|tmp)$", pack, StringComparison.Ordinal);
        Assert.Contains("ef-baseline", pack, StringComparison.OrdinalIgnoreCase);
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
