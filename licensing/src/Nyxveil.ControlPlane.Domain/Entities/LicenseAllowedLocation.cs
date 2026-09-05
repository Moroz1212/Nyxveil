using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class LicenseAllowedLocation
{
    public Guid LicenseId { get; set; }

    /// <summary>Canonical NVP/1 location security identifier (<see cref="Location.LocationId"/>).</summary>
    [MaxLength(64)]
    public string LocationId { get; set; } = string.Empty;

    public License License { get; set; } = null!;
}
