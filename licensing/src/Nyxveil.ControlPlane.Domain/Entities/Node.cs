using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class Node
{
    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    [MaxLength(64)]
    public string LocationId { get; set; } = string.Empty;

    [MaxLength(256)]
    public string DisplayName { get; set; } = string.Empty;

    public NodeRuntimeStatus Status { get; set; } = NodeRuntimeStatus.Offline;

    /// <summary>Admin lifecycle. Deleted/Revoked exclude from catalog and block resurrection.</summary>
    public NodeLifecycleState LifecycleState { get; set; } = NodeLifecycleState.Active;

    public bool Enabled { get; set; } = true;

    public bool TestOnly { get; set; }

    public bool Draining { get; set; }

    public ushort ProtocolVersion { get; set; }

    [MaxLength(64)]
    public string? ServerVersion { get; set; }

    [MaxLength(256)]
    public string? ServerName { get; set; }

    public byte[]? SpkiPin { get; set; }

    /// <summary>Ed25519 public identity (32 bytes).</summary>
    public byte[] PublicIdentity { get; set; } = Array.Empty<byte>();

    public int Capacity { get; set; }

    public int CurrentSessions { get; set; }

    [MaxLength(64)]
    public string? HealthStatus { get; set; }

    public DateTime? LastSeenAt { get; set; }

    public DateTime CreatedAt { get; set; }

    public DateTime UpdatedAt { get; set; }

    public long ConfigVersion { get; set; }

    public DateTime? DeletedAt { get; set; }

    [MaxLength(256)]
    public string? DeletedBy { get; set; }

    [MaxLength(512)]
    public string? DeletionReason { get; set; }

    // --- Non-secret node TLS advertisement (from registration/heartbeat) ---

    [MaxLength(32)]
    public string? TlsMode { get; set; }

    [MaxLength(512)]
    public string? CertSubject { get; set; }

    [MaxLength(512)]
    public string? CertIssuer { get; set; }

    [MaxLength(1024)]
    public string? CertSan { get; set; }

    public DateTime? CertNotBefore { get; set; }

    public DateTime? CertNotAfter { get; set; }

    [MaxLength(128)]
    public string? CertThumbprint { get; set; }

    public bool AcmeAutoRenew { get; set; }

    public DateTime? LastRenewalAttempt { get; set; }

    public DateTime? LastSuccessfulRenewal { get; set; }

    public DateTime? NextPlannedRenewal { get; set; }

    [MaxLength(512)]
    public string? LastRenewalError { get; set; }

    public Location Location { get; set; } = null!;

    public ICollection<NodeEndpoint> Endpoints { get; set; } = new List<NodeEndpoint>();

    public ICollection<NodeTransport> Transports { get; set; } = new List<NodeTransport>();
}
