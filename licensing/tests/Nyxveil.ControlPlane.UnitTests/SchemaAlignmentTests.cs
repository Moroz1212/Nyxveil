using System.Text.RegularExpressions;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.EntityFrameworkCore.Migrations;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Ensures create_database.sql stays aligned with EF model / InitialCreate migration.
/// Companion script: scripts/compare-schema.ps1
/// </summary>
public sealed class SchemaAlignmentTests
{
    private static readonly Regex CreateTableRegex = new(
        @"CREATE\s+TABLE\s+(?:dbo\.)?\[?(?<name>[A-Za-z_][A-Za-z0-9_]*)\]?",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant | RegexOptions.Compiled);

    private static readonly string[] MajorTables =
    [
        "Users", "Plans", "Licenses", "Devices", "Locations", "Nodes",
        "NodeEndpoints", "NodeTransports", "NodeHealth", "NodeMetrics",
        "NodeCredentials", "NodeConfigs", "BootstrapTokens", "TicketAudits",
        "Revocations", "CatalogVersions", "SigningKeysMetadata", "AuditLog",
        "SystemSettings", "PaymentEvents", "LicenseAllowedLocations",
        "AspNetRoles", "AspNetUsers", "AspNetUserRoles", "__EFMigrationsHistory"
    ];

    [Fact]
    public void TestDatabaseScriptMatchesEfModel()
    {
        var sqlPath = PreDeployGateTests.FindLicensingFile("database", "create_database.sql");
        var sql = File.ReadAllText(sqlPath);
        var sqlTables = CreateTableRegex.Matches(sql)
            .Select(m => m.Groups["name"].Value)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);

        foreach (var required in MajorTables)
        {
            Assert.True(sqlTables.Contains(required),
                $"create_database.sql missing CREATE TABLE for '{required}'.");
        }

        var options = new DbContextOptionsBuilder<ControlPlaneDbContext>()
            .UseInMemoryDatabase("schema-align-" + Guid.NewGuid().ToString("N"))
            .Options;
        using var db = new ControlPlaneDbContext(options);
        var modelTables = db.Model.GetEntityTypes()
            .Select(e => e.GetTableName())
            .Where(n => !string.IsNullOrWhiteSpace(n))
            .Cast<string>()
            .ToHashSet(StringComparer.OrdinalIgnoreCase);

        // Identity tables + domain tables must appear in bootstrap SQL.
        var missingFromSql = modelTables.Where(t => !sqlTables.Contains(t)).OrderBy(t => t).ToList();
        Assert.True(missingFromSql.Count == 0,
            "EF model tables missing from create_database.sql: " + string.Join(", ", missingFromSql));

        // Migration CreateTable names must also be covered.
        var migrationPath = FindInitialMigrationCs();
        var migrationText = File.ReadAllText(migrationPath);
        var migrationTables = Regex.Matches(
                migrationText,
                @"CreateTable\(\s*name:\s*""(?<name>[^""]+)""",
                RegexOptions.CultureInvariant)
            .Select(m => m.Groups["name"].Value)
            .ToHashSet(StringComparer.OrdinalIgnoreCase);

        var missingFromMigrationCoverage = migrationTables
            .Where(t => !sqlTables.Contains(t))
            .OrderBy(t => t)
            .ToList();
        Assert.True(missingFromMigrationCoverage.Count == 0,
            "InitialCreate CreateTable names missing from create_database.sql: " +
            string.Join(", ", missingFromMigrationCoverage));
    }

    [Fact]
    public void TestMigrationBaselineMatchesCurrentInitialMigration()
    {
        var migrationFile = Path.GetFileName(FindInitialMigrationCs());
        Assert.EndsWith(".cs", migrationFile, StringComparison.OrdinalIgnoreCase);
        var migrationId = Path.GetFileNameWithoutExtension(migrationFile);
        Assert.False(migrationId.EndsWith(".Designer", StringComparison.OrdinalIgnoreCase));

        var sql = File.ReadAllText(PreDeployGateTests.FindLicensingFile("database", "create_database.sql"));
        Assert.Contains(migrationId, sql, StringComparison.Ordinal);
        Assert.Contains($"[MigrationId] = N'{migrationId}'", sql, StringComparison.Ordinal);
        Assert.Contains($"VALUES (N'{migrationId}'", sql, StringComparison.Ordinal);
    }

    [Fact]
    public void TestSqlBaselineExactlyMatchesEfGeneratedDdlAndModel()
    {
        using var db = new ControlPlaneDbContext(new DbContextOptionsBuilder<ControlPlaneDbContext>()
            .UseSqlServer("Server=unused;Database=SchemaOnly;Integrated Security=True").Options);
        Assert.False(db.Database.HasPendingModelChanges());
        var generated = db.GetService<IMigrator>().GenerateScript(options: MigrationsSqlGenerationOptions.Idempotent);
        var sql = File.ReadAllText(PreDeployGateTests.FindLicensingFile("database", "create_database.sql"));
        var body = sql.Split("-- BEGIN EF GENERATED BASELINE")[1].Split("-- END EF GENERATED BASELINE")[0];
        Assert.Equal(generated.Replace("\r\n", "\n").Trim(), body.Replace("\r\n", "\n").Trim());
        var config = db.Model.FindEntityType(typeof(Nyxveil.ControlPlane.Domain.Entities.NodeConfig))!;
        Assert.True(config.FindProperty("ConfigVersion")!.IsConcurrencyToken);
        var nonce = db.Model.FindEntityType(typeof(Nyxveil.ControlPlane.Domain.Entities.NodeRequestNonce))!;
        Assert.Equal(new[] { "NodeId", "NonceHash" }, nonce.FindPrimaryKey()!.Properties.Select(p => p.Name));
    }

    private static string FindInitialMigrationCs()
    {
        var migrationsDir = Path.Combine(
            PreDeployGateTests.FindLicensingRoot(),
            "src",
            "Nyxveil.ControlPlane.Infrastructure",
            "Persistence",
            "Migrations");
        Assert.True(Directory.Exists(migrationsDir), migrationsDir);

        var files = Directory.GetFiles(migrationsDir, "*_InitialCreate.cs")
            .Where(f => !f.EndsWith(".Designer.cs", StringComparison.OrdinalIgnoreCase))
            .OrderBy(f => f, StringComparer.Ordinal)
            .ToList();
        Assert.True(files.Count >= 1, "No *_InitialCreate.cs migration found under " + migrationsDir);
        return files[^1];
    }
}
