using System.Runtime.Versioning;
using System.Text;
using Microsoft.Data.SqlClient;
using Microsoft.Extensions.Configuration;
using System.Security.Cryptography;
using ProtectedData = System.Security.Cryptography.ProtectedData;

namespace Nyxveil.ControlPlane.Infrastructure.Configuration;

/// <summary>
/// Reads DPAPI-protected secret files from ProgramData and maps them into configuration.
/// Expected files under {programDataPath}/secrets/:
///   license-kek.dpapi → Security:LicenseKekHex
///   sql-password.dpapi → ConnectionStrings overlay (SqlPassword + Password on ControlPlane CS)
/// </summary>
public sealed class DpapiSecretsConfigurationSource : IConfigurationSource
{
    public required string ProgramDataPath { get; init; }

    public IConfigurationProvider Build(IConfigurationBuilder builder) =>
        new DpapiSecretsConfigurationProvider(this);
}

public sealed class DpapiSecretsConfigurationProvider : ConfigurationProvider
{
    public const string LicenseKekFileName = "license-kek.dpapi";
    public const string SqlPasswordFileName = "sql-password.dpapi";

    private readonly DpapiSecretsConfigurationSource _source;

    public DpapiSecretsConfigurationProvider(DpapiSecretsConfigurationSource source)
    {
        _source = source;
    }

    [SupportedOSPlatform("windows")]
    private void LoadWindowsSecrets()
    {
        var data = new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase);
        var secretsDir = Path.Combine(_source.ProgramDataPath, "secrets");
        if (!Directory.Exists(secretsDir))
        {
            Data = data;
            return;
        }

        TryMapFile(secretsDir, LicenseKekFileName, "Security:LicenseKekHex", data);

        if (TryReadSecret(secretsDir, SqlPasswordFileName, out var sqlPassword))
        {
            data["ConnectionStrings:SqlPassword"] = sqlPassword;
            data["Database:SqlPassword"] = sqlPassword;
        }

        Data = data;
    }

    public override void Load()
    {
        if (!OperatingSystem.IsWindows())
        {
            Data = new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase);
            return;
        }

        LoadWindowsSecrets();
    }

    /// <summary>
    /// Applies <c>ConnectionStrings:SqlPassword</c> onto an existing SQL connection string.
    /// </summary>
    public static string ApplySqlPasswordOverlay(string connectionString, string? sqlPassword)
    {
        if (string.IsNullOrWhiteSpace(connectionString) || string.IsNullOrWhiteSpace(sqlPassword))
            return connectionString;

        var builder = new SqlConnectionStringBuilder(connectionString)
        {
            Password = sqlPassword
        };
        return builder.ConnectionString;
    }

    [SupportedOSPlatform("windows")]
    private static void TryMapFile(
        string secretsDir,
        string fileName,
        string configurationKey,
        Dictionary<string, string?> data)
    {
        if (TryReadSecret(secretsDir, fileName, out var value))
            data[configurationKey] = value;
    }

    [SupportedOSPlatform("windows")]
    private static bool TryReadSecret(string secretsDir, string fileName, out string value)
    {
        value = string.Empty;
        if (!OperatingSystem.IsWindows())
            return false;

        var path = Path.Combine(secretsDir, fileName);
        if (!File.Exists(path))
            return false;

        var protectedBytes = File.ReadAllBytes(path);
        if (protectedBytes.Length == 0)
            return false;

        var plain = ProtectedData.Unprotect(
            protectedBytes,
            optionalEntropy: null,
            DataProtectionScope.LocalMachine);
        value = Encoding.UTF8.GetString(plain).TrimEnd('\0', '\r', '\n');
        return value.Length > 0;
    }
}

public static class ProtectedSecretsConfigurationExtensions
{
    /// <summary>
    /// Loads DPAPI secrets from <paramref name="programDataPath"/>/secrets/*.dpapi
    /// (typically C:\ProgramData\Nyxveil\ControlPlane).
    /// </summary>
    public static IConfigurationBuilder AddNyxveilProtectedSecrets(
        this IConfigurationBuilder builder,
        string programDataPath)
    {
        ArgumentNullException.ThrowIfNull(builder);
        ArgumentException.ThrowIfNullOrWhiteSpace(programDataPath);

        return builder.Add(new DpapiSecretsConfigurationSource
        {
            ProgramDataPath = programDataPath
        });
    }
}
