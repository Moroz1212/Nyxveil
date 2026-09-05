namespace Nyxveil.ControlPlane.Application.Options;

public sealed class NodeHeartbeatOptions
{
    public const string SectionName = "NodeHeartbeat";

    public int IntervalSeconds { get; set; } = 30;

    public int DegradedAfterSeconds { get; set; } = 90;

    public int OfflineAfterSeconds { get; set; } = 180;
}
