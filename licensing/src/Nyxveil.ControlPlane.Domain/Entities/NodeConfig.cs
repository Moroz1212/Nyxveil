using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeConfig
{
    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    public bool Enabled { get; set; } = true;

    public bool Draining { get; set; }

    public bool MaintenanceMode { get; set; }

    [MaxLength(8000)]
    public string TransportPolicyJson { get; set; } = "{}";

    [MaxLength(8000)]
    public string? EchPolicyJson { get; set; }

    public int? Mtu { get; set; }

    public int Capacity { get; set; }

    [MaxLength(64)]
    public string? MinimumServerVersion { get; set; }

    public ushort? MinimumProtocolVersion { get; set; }

    public long ConfigVersion { get; set; }

    public DateTime UpdatedAt { get; set; }

    public Node Node { get; set; } = null!;
}
