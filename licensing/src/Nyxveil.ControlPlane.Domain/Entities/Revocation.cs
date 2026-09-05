using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class Revocation
{
    public Guid Id { get; set; }

    public RevocationType Type { get; set; }

    [MaxLength(256)]
    public string TargetId { get; set; } = string.Empty;

    [MaxLength(1024)]
    public string? Reason { get; set; }

    public DateTime CreatedAt { get; set; }

    [MaxLength(256)]
    public string CreatedBy { get; set; } = string.Empty;

    public long Version { get; set; }
}
