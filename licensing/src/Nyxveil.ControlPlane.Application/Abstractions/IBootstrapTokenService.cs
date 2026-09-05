using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IBootstrapTokenService
{
    Task<CreateBootstrapTokenResponse> CreateAsync(CreateBootstrapTokenRequest request, CancellationToken cancellationToken = default);

    Task<IReadOnlyList<BootstrapTokenListItemDto>> ListAsync(CancellationToken cancellationToken = default);
}
