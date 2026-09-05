using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeEndpoint
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    [MaxLength(256)]
    public string Host { get; set; } = string.Empty;

    public int Port { get; set; }

    /// <summary>ipv4, ipv6, or hostname.</summary>
    [MaxLength(16)]
    public string AddressFamily { get; set; } = "hostname";

    public int Priority { get; set; }

    public bool Enabled { get; set; } = true;

    public Node Node { get; set; } = null!;
}
