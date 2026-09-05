using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class SigningKeyMetadata
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string KeyId { get; set; } = string.Empty;

    public byte[] PublicKey { get; set; } = Array.Empty<byte>();

    /// <summary>DPAPI-encrypted private key blob.</summary>
    public byte[] ProtectedPrivateKey { get; set; } = Array.Empty<byte>();

    public SigningKeyStatus Status { get; set; } = SigningKeyStatus.Next;

    public DateTime CreatedAt { get; set; }

    public DateTime? RetiredAt { get; set; }
}
