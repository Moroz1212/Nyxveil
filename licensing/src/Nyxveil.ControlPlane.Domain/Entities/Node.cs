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

    public Location Location { get; set; } = null!;

    public ICollection<NodeEndpoint> Endpoints { get; set; } = new List<NodeEndpoint>();

    public ICollection<NodeTransport> Transports { get; set; } = new List<NodeTransport>();
}
