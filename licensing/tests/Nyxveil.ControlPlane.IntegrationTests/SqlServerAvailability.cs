using System.Text.RegularExpressions;
using Microsoft.Data.SqlClient;
using Xunit;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class ConditionalSqlServerFactAttribute : FactAttribute
{
    public ConditionalSqlServerFactAttribute()
    {
        if (!SqlServerAvailability.IsAvailable)
            Skip = SqlServerAvailability.SkipReason;
    }
}

[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class SqlServerIntegrationCollection
{
    public const string Name = "Real SQL Server";
}

internal static class SqlServerAvailability
{
    private static readonly Lazy<(string? ConnectionString, string? Error)> Detection = new(Detect);
    private static readonly Regex GoLine = new(
        @"^\s*GO(?:\s+(?<count>\d+))?\s*(?:--.*)?$",
        RegexOptions.Multiline | RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);

    internal static bool IsAvailable => Detection.Value.ConnectionString is not null;
    internal static string SkipReason => Detection.Value.Error ??
        "Set NYXVEIL_SQLTEST_CONNECTION or NYXVEIL_SQLTEST_SERVER to run real SQL Server tests.";
    internal static string MasterConnectionString => Detection.Value.ConnectionString ??
        throw new InvalidOperationException(SkipReason);

    internal static string LicensingRoot
    {
        get
        {
            var directory = new DirectoryInfo(AppContext.BaseDirectory);
            while (directory is not null)
            {
                if (File.Exists(Path.Combine(directory.FullName, "database", "migrations",
                        "002_node_lifecycle_cert_metadata.sql")))
                    return directory.FullName;
                directory = directory.Parent;
            }

            throw new DirectoryNotFoundException("Could not locate the licensing root.");
        }
    }

    internal static string Migration002Path => Path.Combine(
        LicensingRoot, "database", "migrations", "002_node_lifecycle_cert_metadata.sql");

    internal static string FixturePath(string fileName) => Path.Combine(
        LicensingRoot, "database", "migrations", "fixtures", fileName);

    internal static async Task<string> CreateDatabaseAsync()
    {
        var databaseName = "NyxveilCpMigTest_" + Guid.NewGuid().ToString("N");
        await ExecuteAsync(MasterConnectionString, $"CREATE DATABASE [{databaseName}];");
        return databaseName;
    }

    internal static async Task DropDatabaseAsync(string databaseName)
    {
        SqlConnection.ClearAllPools();
        var quoted = QuoteIdentifier(databaseName);
        var literal = EscapeLiteral(databaseName);
        await ExecuteAsync(MasterConnectionString, $"""
            USE [master];
            IF DB_ID(N'{literal}') IS NOT NULL
            BEGIN
                ALTER DATABASE {quoted} SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
                DROP DATABASE {quoted};
            END;
            """);
    }

    internal static string DatabaseConnectionString(string databaseName)
    {
        var builder = new SqlConnectionStringBuilder(MasterConnectionString)
        {
            InitialCatalog = databaseName
        };
        return builder.ConnectionString;
    }

    internal static async Task ExecuteFileAsync(string connectionString, string path) =>
        await ExecuteBatchesAsync(connectionString, await File.ReadAllTextAsync(path));

    internal static async Task ExecuteBatchesAsync(string connectionString, string sql)
    {
        var start = 0;
        foreach (Match match in GoLine.Matches(sql))
        {
            var batch = sql[start..match.Index];
            var count = match.Groups["count"].Success
                ? int.Parse(match.Groups["count"].Value, System.Globalization.CultureInfo.InvariantCulture)
                : 1;
            for (var i = 0; i < count; i++)
                await ExecuteAsync(connectionString, batch);
            start = match.Index + match.Length;
        }

        await ExecuteAsync(connectionString, sql[start..]);
    }

    internal static async Task ExecuteAsync(string connectionString, string sql)
    {
        if (string.IsNullOrWhiteSpace(sql))
            return;
        await using var connection = new SqlConnection(connectionString);
        await connection.OpenAsync();
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.CommandTimeout = 120;
        await command.ExecuteNonQueryAsync();
    }

    internal static async Task<T> ScalarAsync<T>(string connectionString, string sql)
    {
        await using var connection = new SqlConnection(connectionString);
        await connection.OpenAsync();
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.CommandTimeout = 120;
        var value = await command.ExecuteScalarAsync();
        if (value is null or DBNull)
        {
            if (default(T) is null)
                return default!;
            throw new InvalidOperationException("SQL scalar returned NULL.");
        }
        return (T)Convert.ChangeType(value, Nullable.GetUnderlyingType(typeof(T)) ?? typeof(T),
            System.Globalization.CultureInfo.InvariantCulture);
    }

    internal static string QuoteIdentifier(string value) => $"[{value.Replace("]", "]]")}]";
    internal static string EscapeLiteral(string value) => value.Replace("'", "''");

    private static (string? ConnectionString, string? Error) Detect()
    {
        var full = Environment.GetEnvironmentVariable("NYXVEIL_SQLTEST_CONNECTION");
        if (!string.IsNullOrWhiteSpace(full))
            return TryOpen(full);

        var configuredServer = Environment.GetEnvironmentVariable("NYXVEIL_SQLTEST_SERVER");
        var candidates = new List<string>();
        if (!string.IsNullOrWhiteSpace(configuredServer))
            candidates.Add(configuredServer!);
        // Local disposable instances used for CP deploy-hardening CI/lab runs.
        candidates.AddRange([
            @"(localdb)\MSSQLLocalDB",
            @".\SQLEXPRESS",
            "localhost\\SQLEXPRESS",
            "localhost",
            "."
        ]);

        Exception? last = null;
        foreach (var server in candidates.Distinct(StringComparer.OrdinalIgnoreCase))
        {
            var builder = new SqlConnectionStringBuilder
            {
                DataSource = server,
                IntegratedSecurity = true,
                Encrypt = true,
                TrustServerCertificate = true,
                ConnectTimeout = 3
            };
            var result = TryOpen(builder.ConnectionString);
            if (result.ConnectionString is not null)
                return result;
            last = new InvalidOperationException(result.Error);
        }

        return (null,
            configuredServer is null
                ? "No local SQL Server detected. Set NYXVEIL_SQLTEST_CONNECTION or NYXVEIL_SQLTEST_SERVER."
                : $"Configured SQL Server is unavailable ({last?.Message ?? "unknown"}).");
    }

    private static (string? ConnectionString, string? Error) TryOpen(string connectionString)
    {
        try
        {
            var builder = new SqlConnectionStringBuilder(connectionString)
            {
                InitialCatalog = "master"
            };
            if (builder.ConnectTimeout <= 0 || builder.ConnectTimeout > 5)
                builder.ConnectTimeout = 5;

            using var connection = new SqlConnection(builder.ConnectionString);
            connection.Open();
            using var command = connection.CreateCommand();
            command.CommandText = "SELECT 1";
            command.CommandTimeout = 5;
            _ = command.ExecuteScalar();
            return (builder.ConnectionString, null);
        }
        catch (Exception exception)
        {
            return (null, $"SQL Server unavailable ({exception.GetType().Name}: {exception.Message})");
        }
    }
}
