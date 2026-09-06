using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Central managed-config mutations for VPN nodes.
/// <see cref="Domain.Entities.NodeConfig"/> is authoritative; <see cref="Domain.Entities.Node"/> is a synced projection.
/// </summary>
public interface INodeManagementService
{
    Task SetEnabledAsync(string nodeId, bool enabled, string actor, CancellationToken cancellationToken = default);

    Task SetDrainingAsync(string nodeId, bool draining, string actor, CancellationToken cancellationToken = default);

    Task EnterMaintenanceAsync(string nodeId, string actor, CancellationToken cancellationToken = default);

    Task ExitMaintenanceAsync(string nodeId, string actor, CancellationToken cancellationToken = default);

    Task SetCapacityAsync(string nodeId, int capacity, string actor, CancellationToken cancellationToken = default);

    Task SetTestOnlyAsync(string nodeId, bool testOnly, string actor, CancellationToken cancellationToken = default);

    Task ChangeLocationAsync(string nodeId, string locationIdOrCode, string actor, CancellationToken cancellationToken = default);

    Task SoftDeleteAsync(string nodeId, string actor, string? reason, bool force = false, CancellationToken cancellationToken = default);

    Task RevokeAsync(string nodeId, string actor, string? reason, bool force = false, CancellationToken cancellationToken = default);

    Task<NodeDecommissionPreview> GetDecommissionPreviewAsync(string nodeId, CancellationToken cancellationToken = default);

    Task<NodeConfigResponse> GetAuthoritativeConfigAsync(string nodeId, CancellationToken cancellationToken = default);
}

public sealed class NodeDecommissionPreview
{
    public string NodeId { get; set; } = string.Empty;
    public int CurrentSessions { get; set; }
    public int EndpointCount { get; set; }
    public int MetricCount { get; set; }
    public string ImpactSummary { get; set; } = string.Empty;
}
