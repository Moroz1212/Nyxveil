using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class Plan
{
    public Guid PlanId { get; set; }

    [MaxLength(64)]
    public string Code { get; set; } = string.Empty;

    [MaxLength(128)]
    public string Name { get; set; } = string.Empty;

    [MaxLength(64)]
    public string Status { get; set; } = "Active";

    public int DurationDays { get; set; }

    public int MaxDevices { get; set; }

    /// <summary>JSON array of location codes, or "*" for all.</summary>
    [MaxLength(4000)]
    public string AllowedLocationsPolicy { get; set; } = "[]";

    /// <summary>JSON array of permission strings.</summary>
    [MaxLength(4000)]
    public string Permissions { get; set; } = "[]";

    public DateTime CreatedAt { get; set; }

    public DateTime UpdatedAt { get; set; }
}
