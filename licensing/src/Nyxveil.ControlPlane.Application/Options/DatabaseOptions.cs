using System.Text.RegularExpressions;

namespace Nyxveil.ControlPlane.Application.Options;

public sealed class DatabaseOptions
{
    public const string SectionName = "Database";

    /// <summary>
    /// Mirrors Deploy.psm1 Assert-ValidDatabaseName:
    /// letter/underscore start; letters, digits, underscore, hyphen; max 128 chars.
    /// </summary>
    public static readonly Regex DatabaseNamePattern =
        new("^[A-Za-z_][A-Za-z0-9_-]{0,127}$", RegexOptions.CultureInvariant | RegexOptions.Compiled);

    /// <summary>SQL Server connection string.</summary>
    public string ConnectionString { get; set; } = string.Empty;

    public int CommandTimeoutSeconds { get; set; } = 30;

    public bool EnableSensitiveDataLogging { get; set; }

    public bool EnableDetailedErrors { get; set; }

    /// <summary>Apply pending EF migrations on startup (dev/ops controlled).</summary>
    public bool MigrateOnStartup { get; set; }

    /// <summary>
    /// When building the SQL connection string, require encryption (default true).
    /// </summary>
    public bool Encrypt { get; set; } = true;

    /// <summary>
    /// Maps to TrustServerCertificate. Null/false is the remote-safe default (do not trust).
    /// Local lab/dev may set true explicitly. Connection-string TrustServerCertificate alone is not enough —
    /// the connection-string provider overrides from this value.
    /// </summary>
    public bool? TrustSqlServerCertificate { get; set; }

    public static bool IsValidDatabaseName(string? databaseName) =>
        !string.IsNullOrWhiteSpace(databaseName) && DatabaseNamePattern.IsMatch(databaseName);
}
