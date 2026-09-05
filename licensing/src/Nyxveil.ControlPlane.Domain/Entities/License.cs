using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class License
{
    public Guid LicenseId { get; set; }

    public Guid? UserId { get; set; }

    [MaxLength(200)]
    public string LicenseKeyVerifier { get; set; } = string.Empty;

    [MaxLength(64)]
    public string Role { get; set; } = string.Empty;

    public Guid PlanId { get; set; }

    public LicenseStatus Status { get; set; } = LicenseStatus.Pending;

    public DateTime CreatedAt { get; set; }

    public DateTime? ActivatedAt { get; set; }

    public DateTime? ExpiresAt { get; set; }

    public int MaxDevices { get; set; }

    [MaxLength(1024)]
    public string? Note { get; set; }

    [MaxLength(256)]
    public string? ExternalPaymentId { get; set; }

    [MaxLength(256)]
    public string CreatedBy { get; set; } = string.Empty;

    public DateTime UpdatedAt { get; set; }

    public Plan Plan { get; set; } = null!;

    public ICollection<Device> Devices { get; set; } = new List<Device>();

    public ICollection<LicenseAllowedLocation> AllowedLocations { get; set; } = new List<LicenseAllowedLocation>();
}
