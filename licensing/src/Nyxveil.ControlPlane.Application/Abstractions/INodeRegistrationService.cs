using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface INodeRegistrationService
{
    Task<NodeRegisterResponse> RegisterWithBootstrapAsync(NodeRegisterRequest request, CancellationToken cancellationToken = default);

    Task<NodeConfigResponse> GetConfigAsync(string nodeId, CancellationToken cancellationToken = default);

    /// <summary>
    /// NodeAuth-only SPKI pin update. Mutates only Node.SpkiPin (+ UpdatedAt).
    /// </summary>
    Task<UpdateNodeSpkiResponse> UpdateSpkiAsync(
        string authenticatedNodeId,
        byte[] spkiPin,
        CancellationToken cancellationToken = default);
}
