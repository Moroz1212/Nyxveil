using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ISigningKeyService
{
    Task<SigningMaterialDto> GetCurrentSigningMaterialAsync(CancellationToken cancellationToken = default);

    Task<RotateSigningKeyResult> RotateAsync(CancellationToken cancellationToken = default);

    Task<IReadOnlyList<VerificationKeyDto>> GetVerificationKeysAsync(CancellationToken cancellationToken = default);

    Task<IReadOnlyList<SigningKeyAdminDto>> ListAllKeysForAdminAsync(CancellationToken cancellationToken = default);

    Task<int> FinalizeExpiredRetiringAsync(CancellationToken cancellationToken = default);
}

public sealed class SigningKeyAdminDto
{
    public string KeyId { get; set; } = string.Empty;
    public byte[] PublicKey { get; set; } = Array.Empty<byte>();
    public string Status { get; set; } = string.Empty;
    public DateTime CreatedAt { get; set; }
    public DateTime? PromotedAt { get; set; }
    public DateTime? RetireAfter { get; set; }
    public DateTime? RetiredAt { get; set; }
}
