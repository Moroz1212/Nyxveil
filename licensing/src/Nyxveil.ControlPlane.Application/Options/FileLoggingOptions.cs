namespace Nyxveil.ControlPlane.Application.Options;

/// <summary>
/// Optional rotating file logging under ProgramData when enabled.
/// Bound from <c>Logging:File</c>.
/// </summary>
public sealed class FileLoggingOptions
{
    public const string SectionName = "Logging:File";

    /// <summary>When true, write logs to <see cref="Directory"/>.</summary>
    public bool Enabled { get; set; }

    /// <summary>
    /// Log directory. Default: %PROGRAMDATA%\Nyxveil\ControlPlane\logs
    /// </summary>
    public string Directory { get; set; } = string.Empty;

    /// <summary>File name prefix (date suffix appended).</summary>
    public string FilePrefix { get; set; } = "controlplane";

    /// <summary>Maximum size of a single log file in megabytes before rotation.</summary>
    public int MaxFileSizeMB { get; set; } = 10;

    /// <summary>Number of rotated files to retain.</summary>
    public int MaxRetainedFiles { get; set; } = 14;
}
