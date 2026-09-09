using Microsoft.Data.SqlClient;

namespace Nyxveil.ControlPlane.IntegrationTests;

[Collection(SqlServerIntegrationCollection.Name)]
public sealed class Migration005SqlServerTests
{
    private static string Migration005Path => Path.Combine(
        SqlServerAvailability.LicensingRoot, "database", "migrations", "005_certificate_operation_states.sql");

    private static string ValidateV5Path => Path.Combine(
        SqlServerAvailability.LicensingRoot, "database", "migrations", "validate_schema_v5.sql");

    private static string CreateDatabasePath => Path.Combine(
        SqlServerAvailability.LicensingRoot, "database", "create_database.sql");

    [ConditionalSqlServerFact]
    public async Task TestFreshCreateDatabaseReachesSchemaV5()
    {
        var database = await SqlServerAvailability.CreateDatabaseAsync();
        try
        {
            // create_database.sql uses sqlcmd $(DatabaseName); apply EF body against the disposable DB
            // by rewriting USE/setvar is awkward — instead run EF baseline via file after USE.
            await ApplyCreateDatabaseToExistingDbAsync(database);

            var connection = SqlServerAvailability.DatabaseConnectionString(database);
            Assert.Equal(5, await SqlServerAvailability.ScalarAsync<int>(
                connection, "SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC;"));
            Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection, """
                SELECT COUNT(1) FROM dbo.__EFMigrationsHistory
                WHERE MigrationId = N'20260909160000_CertificateOperationStates';
                """));
            await SqlServerAvailability.ExecuteFileAsync(connection, ValidateV5Path);
        }
        finally
        {
            await SqlServerAvailability.DropDatabaseAsync(database);
        }
    }

    [ConditionalSqlServerFact]
    public async Task TestSchemaV4ToV5ThenNoOp()
    {
        var database = await SqlServerAvailability.CreateDatabaseAsync();
        try
        {
            await ApplyCreateDatabaseToExistingDbAsync(database);
            var connection = SqlServerAvailability.DatabaseConnectionString(database);

            // Downgrade operational marker to 4 and tighten CK to 0..7 to simulate pre-1.3.2.
            await SqlServerAvailability.ExecuteAsync(connection, """
                UPDATE dbo.NyxveilSchemaVersion SET Version = 4, AppliedAt = SYSUTCDATETIME();
                IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_CertificateRenewalOperations_Status')
                    ALTER TABLE dbo.CertificateRenewalOperations DROP CONSTRAINT CK_CertificateRenewalOperations_Status;
                ALTER TABLE dbo.CertificateRenewalOperations WITH CHECK
                    ADD CONSTRAINT CK_CertificateRenewalOperations_Status CHECK ([Status] BETWEEN 0 AND 7);
                DELETE FROM dbo.__EFMigrationsHistory
                WHERE MigrationId = N'20260909160000_CertificateOperationStates';
                """);

            Assert.Equal(4, await SqlServerAvailability.ScalarAsync<int>(
                connection, "SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC;"));

            await SqlServerAvailability.ExecuteFileAsync(connection, Migration005Path);
            Assert.Equal(5, await SqlServerAvailability.ScalarAsync<int>(
                connection, "SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC;"));
            Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection, """
                SELECT COUNT(1) FROM dbo.__EFMigrationsHistory
                WHERE MigrationId = N'20260909160000_CertificateOperationStates';
                """));
            await SqlServerAvailability.ExecuteFileAsync(connection, ValidateV5Path);

            // Idempotent re-run
            await SqlServerAvailability.ExecuteFileAsync(connection, Migration005Path);
            Assert.Equal(5, await SqlServerAvailability.ScalarAsync<int>(
                connection, "SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC;"));
            await SqlServerAvailability.ExecuteFileAsync(connection, ValidateV5Path);
        }
        finally
        {
            await SqlServerAvailability.DropDatabaseAsync(database);
        }
    }

    [ConditionalSqlServerFact]
    public async Task TestSchemaGreaterThan5IsDetectableAsFuture()
    {
        var database = await SqlServerAvailability.CreateDatabaseAsync();
        try
        {
            await ApplyCreateDatabaseToExistingDbAsync(database);
            var connection = SqlServerAvailability.DatabaseConnectionString(database);
            await SqlServerAvailability.ExecuteAsync(connection,
                "UPDATE dbo.NyxveilSchemaVersion SET Version = 99, AppliedAt = SYSUTCDATETIME();");
            Assert.Equal(99, await SqlServerAvailability.ScalarAsync<int>(
                connection, "SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC;"));
            // Deploy script refuses Current > Expected; validator only requires >= 5 so still passes.
            await SqlServerAvailability.ExecuteFileAsync(connection, ValidateV5Path);
        }
        finally
        {
            await SqlServerAvailability.DropDatabaseAsync(database);
        }
    }

    private static async Task ApplyCreateDatabaseToExistingDbAsync(string databaseName)
    {
        var raw = await File.ReadAllTextAsync(CreateDatabasePath);
        // Strip database-create / :setvar / USE $(DatabaseName) bootstrap; keep EF + operational seed.
        var start = raw.IndexOf("-- BEGIN EF GENERATED BASELINE", StringComparison.Ordinal);
        Assert.True(start >= 0);
        var body = raw[start..];
        // Replace any residual $(DatabaseName) just in case.
        body = body.Replace("$(DatabaseName)", databaseName, StringComparison.Ordinal);
        var connection = SqlServerAvailability.DatabaseConnectionString(databaseName);
        await SqlServerAvailability.ExecuteBatchesAsync(connection, body);
    }
}
