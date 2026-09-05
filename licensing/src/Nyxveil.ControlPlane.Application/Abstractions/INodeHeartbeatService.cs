using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface INodeHeartbeatService
{
    Task<NodeHeartbeatResponse> ProcessHeartbeatAsync(NodeHeartbeatRequest request, CancellationToken cancellationToken = default);
}
