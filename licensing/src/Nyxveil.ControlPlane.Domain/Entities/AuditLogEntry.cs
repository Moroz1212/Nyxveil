using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class AuditLogEntry
{
    public Guid Id { get; set; }

    [MaxLength(256)]
    public string Actor { get; set; } = string.Empty;

    [MaxLength(128)]
    public string Action { get; set; } = string.Empty;

    [MaxLength(128)]
    public string EntityType { get; set; } = string.Empty;

    [MaxLength(256)]
    public string? EntityId { get; set; }

    public DateTime Timestamp { get; set; }

    [MaxLength(64)]
    public string? IpAddress { get; set; }

    [MaxLength(8000)]
    public string? DetailsJson { get; set; }
}
