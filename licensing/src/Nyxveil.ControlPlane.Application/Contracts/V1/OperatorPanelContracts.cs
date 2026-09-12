using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public enum AttentionSeverity
{
    Info = 0,
    Warning = 1,
    Critical = 2
}

public sealed class AttentionItem
{
    public AttentionSeverity Severity { get; init; }
    public string Title { get; init; } = "";
    public string Detail { get; init; } = "";
    public string? NodeId { get; init; }
    public string? NodeName { get; init; }
    public string? LocationId { get; init; }
    public string? AgeText { get; init; }
    public string Href { get; init; } = "/";
    public string Kind { get; init; } = "";
}

public sealed class UpdatePreflightResult
{
    public string NodeId { get; init; } = "";
    public string DisplayName { get; init; } = "";
    public string LocationId { get; init; } = "";
    public string? InstalledVersion { get; init; }
    public string? TargetVersion { get; init; }
    public string? ReleaseTag { get; init; }
    public string? HeartbeatAgeText { get; init; }
    public bool HeartbeatFresh { get; init; }
    public string Tun { get; init; } = "Нет данных";
    public string TlsRuntime { get; init; } = "Нет данных";
    public string Quic { get; init; } = "Нет данных";
    public string Cp { get; init; } = "Нет данных";
    public int ActiveSessions { get; init; }
    public int SiblingHealthyCount { get; init; }
    public int SiblingFreeCapacity { get; init; }
    public bool LocationSafe { get; init; }
    public string LocationSafetyDetail { get; init; } = "";
    public int DrainWaitSeconds { get; init; } = 120;
    public string DrainPolicyText { get; init; } = "";
    public bool CanEnqueue { get; init; }
    public string? BlockReason { get; init; }
    public IReadOnlyList<string> Notes { get; init; } = Array.Empty<string>();
}

public sealed class UpdateTimelineStep
{
    public string Id { get; init; } = "";
    public string Title { get; init; } = "";
    public bool Reached { get; init; }
    public bool Current { get; init; }
    public DateTime? AtUtc { get; init; }
    public string? Message { get; init; }
}

public enum LocationRolloutStatus
{
    Pending = 0,
    Running = 1,
    Succeeded = 2,
    Failed = 3,
    Stopped = 4
}

public enum LocationRolloutItemStatus
{
    Pending = 0,
    Running = 1,
    Succeeded = 2,
    Failed = 3,
    Skipped = 4
}
