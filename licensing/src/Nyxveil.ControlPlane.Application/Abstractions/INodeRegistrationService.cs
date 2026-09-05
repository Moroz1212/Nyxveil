using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface INodeRegistrationService
{
    Task<NodeRegisterResponse> RegisterWithBootstrapAsync(NodeRegisterRequest request, CancellationToken cancellationToken = default);

    Task<NodeConfigResponse> GetConfigAsync(string nodeId, CancellationToken cancellationToken = default);
}
