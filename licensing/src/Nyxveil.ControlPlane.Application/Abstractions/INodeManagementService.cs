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

    Task<NodeConfigResponse> GetAuthoritativeConfigAsync(string nodeId, CancellationToken cancellationToken = default);
}
