using Microsoft.Data.SqlClient;

namespace Nyxveil.ControlPlane.IntegrationTests;

[Collection(SqlServerIntegrationCollection.Name)]
public sealed class Migration002SqlServerTests
{
    [ConditionalSqlServerFact]
    public Task TestMigration002FromExactV1ToV2() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await RunMigrationAsync(connection);
        Assert.Equal(2, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id = 1;"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'LifecycleState';"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.NodeHealth') AND name=N'CpConnected';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002SecondRunIsSafe() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await RunMigrationAsync(connection);
        await RunMigrationAsync(connection);
        Assert.Equal(2, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id = 1;"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.indexes WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'IX_Nodes_LifecycleState';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002FromPartialLifecycleStateColumn() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await SqlServerAvailability.ExecuteAsync(connection,
            "ALTER TABLE dbo.Nodes ADD LifecycleState int NOT NULL CONSTRAINT DF_Test_LifecycleState DEFAULT(0);");
        await RunMigrationAsync(connection);
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.check_constraints WHERE parent_object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'CK_Nodes_LifecycleState';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002FromPartialCertificateColumns() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await SqlServerAvailability.ExecuteAsync(connection, """
            ALTER TABLE dbo.Nodes ADD
                CertSubject nvarchar(512) NULL,
                CertNotAfter datetime2 NULL,
                CertThumbprint nvarchar(128) NULL;
            """);
        await RunMigrationAsync(connection);
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'CertIssuer';"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'CertThumbprint';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002FromPartialIndexes() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await SqlServerAvailability.ExecuteAsync(connection, """
            ALTER TABLE dbo.Nodes ADD LifecycleState int NOT NULL
                CONSTRAINT DF_Test_LifecycleIndex DEFAULT(0);
            CREATE INDEX IX_Nodes_LifecycleState ON dbo.Nodes(LifecycleState);
            CREATE INDEX IX_Nodes_Test_Id ON dbo.Nodes(Id);
            """);
        await RunMigrationAsync(connection);
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.indexes WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'IX_Nodes_LifecycleState';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002FromMixedPartialState() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await SqlServerAvailability.ExecuteAsync(connection, """
            ALTER TABLE dbo.Nodes ADD
                LifecycleState int NOT NULL CONSTRAINT DF_Test_MixedLifecycle DEFAULT(0),
                DeletedAt datetime2 NULL,
                CertIssuer nvarchar(512) NULL,
                AcmeAutoRenew bit NOT NULL CONSTRAINT DF_Test_MixedRenew DEFAULT(0);
            ALTER TABLE dbo.NodeHealth ADD TunReady bit NULL, QuicOk bit NULL;
            CREATE INDEX IX_Nodes_LifecycleState ON dbo.Nodes(LifecycleState);
            """);
        await RunMigrationAsync(connection);
        Assert.Equal(2, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id=1;"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.NodeHealth') AND name=N'TicketKeysLoaded';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigration002RejectsIncompatibleExistingObject() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await SqlServerAvailability.ExecuteAsync(connection,
            "ALTER TABLE dbo.Nodes ADD LifecycleState nvarchar(12) NOT NULL CONSTRAINT DF_Test_BadLifecycle DEFAULT(N'active');");
        var error = await Assert.ThrowsAsync<SqlException>(() => RunMigrationAsync(connection));
        Assert.Contains("Incompatible dbo.Nodes.LifecycleState", error.Message, StringComparison.OrdinalIgnoreCase);
    });

    [ConditionalSqlServerFact]
    public Task TestSchemaVersionChangesOnlyAfterCompleteSuccess() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id=1;"));
        await SqlServerAvailability.ExecuteAsync(connection,
            "ALTER TABLE dbo.Nodes ADD LifecycleState nvarchar(12) NOT NULL CONSTRAINT DF_Test_BadVersion DEFAULT(N'active');");
        await Assert.ThrowsAsync<SqlException>(() => RunMigrationAsync(connection));
        // Fail-closed before version bump: fixture v1 row must remain unchanged.
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id=1;"));
        Assert.Equal(0, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.check_constraints WHERE name=N'CK_Nodes_LifecycleState';"));
    });

    [ConditionalSqlServerFact]
    public Task TestMigrationFailureRollsBackTransactionalChanges() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        var migration = await File.ReadAllTextAsync(SqlServerAvailability.Migration002Path);
        migration = migration.Replace(
            "IF EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Id = 1)",
            "THROW 50199, N'Injected failure after structural validation.', 1;\r\n" +
            "    IF EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Id = 1)",
            StringComparison.Ordinal);
        var error = await Assert.ThrowsAsync<SqlException>(
            () => SqlServerAvailability.ExecuteBatchesAsync(connection, migration));
        Assert.Equal(50199, error.Number);
        Assert.Equal(0, await SqlServerAvailability.ScalarAsync<int>(connection,
            "SELECT COUNT(*) FROM sys.columns WHERE object_id=OBJECT_ID(N'dbo.Nodes') AND name=N'LifecycleState';"));
        Assert.Equal(1, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT Version FROM dbo.NyxveilSchemaVersion WHERE Id=1;"));
    });

    [ConditionalSqlServerFact]
    public Task TestLifecycleStateCanBeReferencedAfterCreation() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        await RunMigrationAsync(connection);
        await SqlServerAvailability.ExecuteAsync(connection, """
            INSERT INTO dbo.Nodes DEFAULT VALUES;
            UPDATE dbo.Nodes SET LifecycleState=2 WHERE Id=1;
            """);
        Assert.Equal(2, await SqlServerAvailability.ScalarAsync<int>(
            connection, "SELECT LifecycleState FROM dbo.Nodes WHERE Id=1;"));
    });

    [ConditionalSqlServerFact]
    public Task TestBrokenLifecycleSameBatchStillFailsWithMsg207() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        var error = await Assert.ThrowsAsync<SqlException>(() =>
            SqlServerAvailability.ExecuteFileAsync(connection,
                SqlServerAvailability.FixturePath("002_broken_lifecycle_same_batch.sql")));
        Assert.True(error.Number == 207 || error.Errors.Cast<SqlError>().Any(item => item.Number == 207),
            $"Expected SQL Server Msg 207, got {error.Number}.");
        Assert.Contains("Invalid column name", error.Message, StringComparison.OrdinalIgnoreCase);
    });

    [ConditionalSqlServerFact]
    public Task TestFixedMigration002DoesNotHitMsg207() => InDatabaseAsync(async connection =>
    {
        await CreateV1Async(connection);
        var error = await Record.ExceptionAsync(() => RunMigrationAsync(connection));
        Assert.Null(error);
    });

    private static Task CreateV1Async(string connection) =>
        SqlServerAvailability.ExecuteFileAsync(connection,
            SqlServerAvailability.FixturePath("v1_nodes_nodehealth_minimal.sql"));

    private static Task RunMigrationAsync(string connection) =>
        SqlServerAvailability.ExecuteFileAsync(connection, SqlServerAvailability.Migration002Path);

    private static async Task InDatabaseAsync(Func<string, Task> test)
    {
        var database = await SqlServerAvailability.CreateDatabaseAsync();
        try
        {
            await test(SqlServerAvailability.DatabaseConnectionString(database));
        }
        finally
        {
            await SqlServerAvailability.DropDatabaseAsync(database);
        }
    }
}
