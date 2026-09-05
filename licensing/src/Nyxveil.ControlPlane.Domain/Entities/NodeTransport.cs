using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeTransport
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    /// <summary>tls or quic.</summary>
    [MaxLength(16)]
    public string TransportType { get; set; } = "tls";

    public bool Enabled { get; set; } = true;

    public int Priority { get; set; }

    public Node Node { get; set; } = null!;
}
