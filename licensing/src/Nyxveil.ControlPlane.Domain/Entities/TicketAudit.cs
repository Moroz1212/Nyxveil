using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class TicketAudit
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string TicketId { get; set; } = string.Empty;

    public Guid LicenseId { get; set; }

    [MaxLength(128)]
    public string DeviceId { get; set; } = string.Empty;

    public DateTime IssuedAt { get; set; }

    public DateTime ExpiresAt { get; set; }

    [MaxLength(4000)]
    public string LocationsJson { get; set; } = "[]";

    [MaxLength(4000)]
    public string NodeScopeJson { get; set; } = "[]";

    /// <summary>issue or refresh.</summary>
    [MaxLength(32)]
    public string Action { get; set; } = string.Empty;
}
