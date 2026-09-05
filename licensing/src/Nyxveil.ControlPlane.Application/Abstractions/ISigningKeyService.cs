using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ISigningKeyService
{
    Task<SigningMaterialDto> GetCurrentSigningMaterialAsync(CancellationToken cancellationToken = default);

    Task<RotateSigningKeyResult> RotateAsync(CancellationToken cancellationToken = default);

    Task<IReadOnlyList<VerificationKeyDto>> GetVerificationKeysAsync(CancellationToken cancellationToken = default);
}
