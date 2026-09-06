using System.Text.RegularExpressions;
using Microsoft.Data.SqlClient;

namespace Nyxveil.ControlPlane.IntegrationTests;

[Collection(SqlServerIntegrationCollection.Name)]
public sealed class RestoreSqlServerTests
{
    private static string RestoreScript => File.ReadAllText(Path.Combine(
        SqlServerAvailability.LicensingRoot, "scripts", "restore-db.ps1"));
    private static string DeployModule => File.ReadAllText(Path.Combine(
        SqlServerAvailability.LicensingRoot, "scripts", "Nyxveil.ControlPlane.Deploy.psm1"));
    private static string ProductionDeploy => File.ReadAllText(Path.Combine(
        SqlServerAvailability.LicensingRoot, "scripts", "production-deploy.ps1"));

    [Fact]
    public void TestRestoreConnectsToMaster()
    {
        var function = FunctionBody(DeployModule, "Get-NyxveilRestoreMasterSql");
        Assert.Matches(new Regex(
            @"return\s+@""[\s\S]*?USE\s+\[master\];[\s\S]*?RESTORE\s+DATABASE\s+\[\$quotedDb\]",
            RegexOptions.IgnoreCase), function);
        Assert.Matches(@"Invoke-NyxveilSql[\s\S]{0,500}-DatabaseName\s+'master'", RestoreScript);
    }

    [Fact]
    public void TestRestoreDoesNotUseTargetDatabaseSession()
    {
        Assert.DoesNotMatch(new Regex(
            @"Invoke-NyxveilSql[\s\S]{0,500}-DatabaseName\s+\$Database\b",
            RegexOptions.IgnoreCase), RestoreScript);
        Assert.Contains("-DatabaseName 'master'", RestoreScript, StringComparison.OrdinalIgnoreCase);
    }

    [ConditionalSqlServerFact]
    public Task TestRestoreWithActiveTargetConnections() => WithBackupAsync(async context =>
    {
        await using var blocker = new SqlConnection(context.TargetConnection);
        await blocker.OpenAsync();
        await RestoreFromMasterAsync(context);
        Assert.Equal("MULTI_USER", await DatabasePropertyAsync(context.Database, "user_access_desc"));
        Assert.Equal("backup", await ReadStateAsync(context.TargetConnection));
    });

    [Fact]
    public void TestRestoreSetsSingleUserWithRollbackImmediate() =>
        Assert.Contains("SET SINGLE_USER WITH ROLLBACK IMMEDIATE", RestoreScript,
            StringComparison.OrdinalIgnoreCase);

    [ConditionalSqlServerFact]
    public Task TestRestoreReturnsMultiUser() => WithBackupAsync(async context =>
    {
        await RestoreFromMasterAsync(context);
        Assert.Equal("MULTI_USER", await DatabasePropertyAsync(context.Database, "user_access_desc"));
    });

    [ConditionalSqlServerFact]
    public Task TestRestoreDatabaseOnline() => WithBackupAsync(async context =>
    {
        await RestoreFromMasterAsync(context);
        Assert.Equal("ONLINE", await DatabasePropertyAsync(context.Database, "state_desc"));
    });

    [ConditionalSqlServerFact]
    public Task TestRestoreExactBackupState() => WithBackupAsync(async context =>
    {
        Assert.Equal("mutated", await ReadStateAsync(context.TargetConnection));
        await RestoreFromMasterAsync(context);
        Assert.Equal("backup", await ReadStateAsync(context.TargetConnection));
    });

    [Fact]
    public void TestRestoreWhileCPServiceStopped()
    {
        var stop = RestoreScript.IndexOf("Stop-Service -Name $ServiceName", StringComparison.OrdinalIgnoreCase);
        var restore = RestoreScript.IndexOf("RESTORE DATABASE", StringComparison.OrdinalIgnoreCase);
        Assert.True(stop >= 0 && restore > stop, "The optional service stop must happen before RESTORE.");
        Assert.Contains("if ($StopService)", RestoreScript, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRestoreFailureDoesNotStartNewApplication()
    {
        var catchStart = RestoreScript.IndexOf(
            "catch {\r\n    if ($enteredSingleUser -and -not $restoreSucceeded)",
            StringComparison.Ordinal);
        Assert.True(catchStart >= 0, "The outer restore failure handler was not found.");
        var catchBlock = RestoreScript[catchStart..];
        Assert.DoesNotContain("Start-Service", catchBlock, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("if ($enteredSingleUser -and -not $restoreSucceeded)", catchBlock, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRollbackReportsDatabaseFailureSeparately()
    {
        Assert.Contains("database=$($rollbackDatabase.ToString().ToLowerInvariant())",
            ProductionDeploy, StringComparison.Ordinal);
        Assert.Contains("$rollbackComplete = $rollbackBinaries -and $rollbackConfig -and $rollbackDatabase",
            ProductionDeploy, StringComparison.Ordinal);
        Assert.Contains("rollback_complete=$($rollbackComplete.ToString().ToLowerInvariant())",
            ProductionDeploy, StringComparison.Ordinal);
    }

    [ConditionalSqlServerFact]
    public Task TestRestoreFromTargetSessionReproducesMsg3102() => WithBackupAsync(async context =>
    {
        await using var target = new SqlConnection(context.TargetConnection);
        await target.OpenAsync();
        await using var command = target.CreateCommand();
        command.CommandTimeout = 120;
        command.CommandText = $"""
            RESTORE DATABASE {SqlServerAvailability.QuoteIdentifier(context.Database)}
            FROM DISK=N'{SqlServerAvailability.EscapeLiteral(context.BackupPath)}'
            WITH REPLACE;
            """;
        var error = await Assert.ThrowsAsync<SqlException>(() => command.ExecuteNonQueryAsync());
        Assert.True(error.Number == 3102 || error.Errors.Cast<SqlError>().Any(item => item.Number == 3102),
            $"Expected SQL Server Msg 3102, got {error.Number}.");
        Assert.Contains("in use by this session", error.Message, StringComparison.OrdinalIgnoreCase);
    });

    private static async Task WithBackupAsync(Func<RestoreContext, Task> test)
    {
        var database = await SqlServerAvailability.CreateDatabaseAsync();
        string? backupPath = null;
        try
        {
            var target = SqlServerAvailability.DatabaseConnectionString(database);
            await SqlServerAvailability.ExecuteAsync(target, """
                CREATE TABLE dbo.RestoreState(Id int NOT NULL PRIMARY KEY, Value nvarchar(32) NOT NULL);
                INSERT INTO dbo.RestoreState VALUES(1, N'backup');
                """);
            var backupDirectory = await SqlServerAvailability.ScalarAsync<string?>(
                SqlServerAvailability.MasterConnectionString,
                "SELECT CONVERT(nvarchar(4000), SERVERPROPERTY('InstanceDefaultBackupPath'));");
            if (string.IsNullOrWhiteSpace(backupDirectory))
            {
                // LocalDB / Express often have no InstanceDefaultBackupPath.
                backupDirectory = Path.Combine(Path.GetTempPath(), "nyxveil-sql-restore-tests");
                Directory.CreateDirectory(backupDirectory);
            }
            var separator = backupDirectory.Contains('/') ? "/" : "\\";
            backupPath = backupDirectory.TrimEnd('\\', '/') + separator + database + ".bak";
            await SqlServerAvailability.ExecuteAsync(SqlServerAvailability.MasterConnectionString, $"""
                BACKUP DATABASE {SqlServerAvailability.QuoteIdentifier(database)}
                TO DISK=N'{SqlServerAvailability.EscapeLiteral(backupPath)}'
                WITH INIT, COPY_ONLY, CHECKSUM;
                RESTORE VERIFYONLY FROM DISK=N'{SqlServerAvailability.EscapeLiteral(backupPath)}'
                WITH CHECKSUM;
                """);
            await SqlServerAvailability.ExecuteAsync(target,
                "UPDATE dbo.RestoreState SET Value=N'mutated' WHERE Id=1;");
            await test(new RestoreContext(database, target, backupPath));
        }
        finally
        {
            await SqlServerAvailability.DropDatabaseAsync(database);
            if (backupPath is not null)
            {
                try
                {
                    await SqlServerAvailability.ExecuteAsync(SqlServerAvailability.MasterConnectionString,
                        $"EXEC master.dbo.xp_delete_file 0, N'{SqlServerAvailability.EscapeLiteral(backupPath)}';");
                }
                catch (SqlException)
                {
                    // Backup cleanup support differs by SQL Server edition/configuration.
                }
            }
        }
    }

    private static async Task RestoreFromMasterAsync(RestoreContext context)
    {
        var database = SqlServerAvailability.QuoteIdentifier(context.Database);
        await SqlServerAvailability.ExecuteAsync(SqlServerAvailability.MasterConnectionString, $"""
            USE [master];
            ALTER DATABASE {database} SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
            RESTORE DATABASE {database}
              FROM DISK=N'{SqlServerAvailability.EscapeLiteral(context.BackupPath)}'
              WITH REPLACE, RECOVERY;
            ALTER DATABASE {database} SET MULTI_USER;
            """);
    }

    private static Task<string> DatabasePropertyAsync(string database, string property) =>
        SqlServerAvailability.ScalarAsync<string>(SqlServerAvailability.MasterConnectionString,
            $"SELECT {property} FROM sys.databases WHERE name=N'{SqlServerAvailability.EscapeLiteral(database)}';");

    private static Task<string> ReadStateAsync(string targetConnection) =>
        SqlServerAvailability.ScalarAsync<string>(
            targetConnection, "SELECT Value FROM dbo.RestoreState WHERE Id=1;");

    private static string FunctionBody(string script, string functionName)
    {
        var start = script.IndexOf($"function {functionName}", StringComparison.OrdinalIgnoreCase);
        Assert.True(start >= 0, $"Function {functionName} was not found.");
        var next = script.IndexOf("\nfunction ", start + 1, StringComparison.OrdinalIgnoreCase);
        return next < 0 ? script[start..] : script[start..next];
    }

    private sealed record RestoreContext(string Database, string TargetConnection, string BackupPath);
}
