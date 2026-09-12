using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public sealed class FleetOverviewDto
{
    public DateTime GeneratedAt { get; set; }
    public FleetSummaryDto Summary { get; set; } = new();
    public IReadOnlyList<FleetLocationCardDto> Locations { get; set; } = Array.Empty<FleetLocationCardDto>();
    public IReadOnlyList<FleetVersionBucketDto> VersionDistribution { get; set; } = Array.Empty<FleetVersionBucketDto>();
    public IReadOnlyList<FleetAttentionItemDto> Attention { get; set; } = Array.Empty<FleetAttentionItemDto>();
}

public sealed class FleetSummaryDto
{
    public int Locations { get; set; }
    public int Nodes { get; set; }
    public int Healthy { get; set; }
    public int Degraded { get; set; }
    public int Offline { get; set; }
    public int Draining { get; set; }
    public int Maintenance { get; set; }
    public int ActiveSessions { get; set; }
    public int? TotalCapacity { get; set; }
    public double? CapacityUsedPercent { get; set; }
    public int UpdateAvailable { get; set; }
    public int CertificateWarnings { get; set; }
}

public enum FleetLocationHealth
{
    Healthy = 0,
    Degraded = 1,
    Critical = 2,
    Offline = 3,
    Maintenance = 4
}

public sealed class FleetLocationCardDto
{
    public string LocationId { get; set; } = "";
    public string Code { get; set; } = "";
    public string DisplayName { get; set; } = "";
    public string Country { get; set; } = "";
    public string City { get; set; } = "";
    public FleetLocationHealth Health { get; set; }
    public int NodeCount { get; set; }
    public int HealthyCount { get; set; }
    public int DegradedCount { get; set; }
    public int OfflineCount { get; set; }
    public int ActiveSessions { get; set; }
    public int? TotalCapacity { get; set; }
    public int? RemainingCapacity { get; set; }
    public double? CapacityUsedPercent { get; set; }
    public string? DominantVersion { get; set; }
    public int UpdateAvailableCount { get; set; }
    public string CertificateAggregate { get; set; } = "Unknown";
    public int? NearestCertDaysRemaining { get; set; }
    public bool HasActiveOperation { get; set; }
    public IReadOnlyList<FleetNodeRowDto> Nodes { get; set; } = Array.Empty<FleetNodeRowDto>();
}

public sealed class FleetNodeRowDto
{
    public string NodeId { get; set; } = "";
    public string DisplayName { get; set; } = "";
    public string LocationId { get; set; } = "";
    public OperatorNodeMode Mode { get; set; }
    public string? ServerVersion { get; set; }
    public int CurrentSessions { get; set; }
    public int Capacity { get; set; }
    public DataFreshness Freshness { get; set; }
    public string FreshnessLabel { get; set; } = "";
    public bool? TunOk { get; set; }
    public bool? TlsOk { get; set; }
    public bool? QuicOk { get; set; }
    public bool? BridgeOk { get; set; }
    public bool? TicketKeysOk { get; set; }
    public bool? CpConnected { get; set; }
    public bool Draining { get; set; }
    public bool Maintenance { get; set; }
    public bool TestOnly { get; set; }
    public bool UpdateAvailable { get; set; }
    public string? ActiveOperation { get; set; }
    public string CertificateHealth { get; set; } = "Unknown";
    public int? CertDaysRemaining { get; set; }
}

public sealed class FleetVersionBucketDto
{
    public string Version { get; set; } = "";
    public int Count { get; set; }
    public bool UpdateAvailable { get; set; }
}

public sealed class FleetAttentionItemDto
{
    public string Severity { get; set; } = "warn";
    public string Kind { get; set; } = "";
    public string Title { get; set; } = "";
    public string? NodeId { get; set; }
    public string? LocationId { get; set; }
    public string? Href { get; set; }
}

public sealed class FleetQuery
{
    public string? Filter { get; set; }
    public string? Search { get; set; }
    public string Sort { get; set; } = "status";
}
