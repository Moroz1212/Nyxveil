using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeHealth
{
    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    public double CpuPercent { get; set; }

    public double MemoryPercent { get; set; }

    public long? MemoryBytes { get; set; }

    public long? UptimeSeconds { get; set; }

    public int ActiveSessions { get; set; }

    public double? NetworkRxRate { get; set; }

    public double? NetworkTxRate { get; set; }

    public double? LoadAverage { get; set; }

    public bool Healthy { get; set; }

    public DateTime UpdatedAt { get; set; }

    public Node Node { get; set; } = null!;
}
