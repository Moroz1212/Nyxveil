using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeCredential
{
    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    public byte[] PublicKey { get; set; } = Array.Empty<byte>();

    public DateTime CredentialIssuedAt { get; set; }

    [MaxLength(256)]
    public string? NodeAuthSecretVerifier { get; set; }

    public DateTime? LastAuthAt { get; set; }

    /// <summary>
    /// Last accepted Frozen Core <c>nvp-node-v1</c> token unix timestamp (anti-replay).
    /// </summary>
    public long? LastCoreTokenUnix { get; set; }

    public Node Node { get; set; } = null!;
}
