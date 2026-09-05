using Microsoft.Data.SqlClient;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Configuration;

namespace Nyxveil.ControlPlane.Infrastructure.Persistence;

/// <summary>
/// Resolves ConnectionStrings:ControlPlane with DPAPI SqlPassword overlay and Database TLS policy.
/// Never logs the connection string.
/// </summary>
public sealed class DatabaseConnectionStringProvider : IDatabaseConnectionStringProvider
{
    private readonly string _connectionString;

    public DatabaseConnectionStringProvider(IConfiguration configuration)
    {
        ArgumentNullException.ThrowIfNull(configuration);

        var raw = configuration.GetConnectionString("ControlPlane")
                  ?? configuration[$"{DatabaseOptions.SectionName}:ConnectionString"]
                  ?? throw new InvalidOperationException(
                      "Connection string 'ControlPlane' or Database:ConnectionString is required.");

        var sqlPassword = configuration["ConnectionStrings:SqlPassword"]
                          ?? configuration["Database:SqlPassword"];

        var withPassword = DpapiSecretsConfigurationProvider.ApplySqlPasswordOverlay(raw, sqlPassword);

        var databaseOptions = configuration.GetSection(DatabaseOptions.SectionName).Get<DatabaseOptions>()
                              ?? new DatabaseOptions();

        _connectionString = ApplyTlsPolicy(withPassword, databaseOptions);
    }

    /// <summary>For tests: construct with an already-resolved connection string.</summary>
    public DatabaseConnectionStringProvider(string connectionString)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(connectionString);
        _connectionString = connectionString;
    }

    /// <summary>
    /// Applies <see cref="DatabaseOptions.Encrypt"/> and <see cref="DatabaseOptions.TrustSqlServerCertificate"/>.
    /// Trust defaults to false (remote-safe) unless explicitly set true — CS TrustServerCertificate alone is ignored.
    /// </summary>
    public static string ApplyTlsPolicy(string connectionString, DatabaseOptions options)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(connectionString);
        ArgumentNullException.ThrowIfNull(options);

        var builder = new SqlConnectionStringBuilder(connectionString)
        {
            Encrypt = options.Encrypt,
            TrustServerCertificate = options.TrustSqlServerCertificate == true
        };

        return builder.ConnectionString;
    }

    public string GetConnectionString() => _connectionString;

    /// <summary>Returns a redacted builder view for diagnostics (never the password).</summary>
    public static string DescribeWithoutPassword(string connectionString)
    {
        try
        {
            var builder = new SqlConnectionStringBuilder(connectionString) { Password = "********" };
            return builder.ConnectionString;
        }
        catch (Exception)
        {
            return "[unparseable-connection-string]";
        }
    }
}
