using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class BootstrapToken
{
    public Guid BootstrapId { get; set; }

    [MaxLength(256)]
    public string Verifier { get; set; } = string.Empty;

    public DateTime ExpiresAt { get; set; }

    public int MaxUses { get; set; }

    public int UsedCount { get; set; }

    [MaxLength(64)]
    public string? AllowedLocation { get; set; }

    public BootstrapTokenStatus Status { get; set; } = BootstrapTokenStatus.Active;

    public DateTime CreatedAt { get; set; }

    [MaxLength(256)]
    public string CreatedBy { get; set; } = string.Empty;

    [MaxLength(1024)]
    public string? Note { get; set; }
}
