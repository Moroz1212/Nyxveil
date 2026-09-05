namespace Nyxveil.ControlPlane.Application.Options;

public sealed class MetricsRetentionOptions
{
    public const string SectionName = "MetricsRetention";

    /// <summary>How long raw/snapshot metric rows are retained.</summary>
    public int RawDays { get; set; } = 30;

    /// <summary>Interval between persisted metric snapshots.</summary>
    public int SnapshotMinutes { get; set; } = 5;
}
