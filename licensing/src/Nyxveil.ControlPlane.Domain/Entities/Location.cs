using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class Location
{
    [MaxLength(64)]
    public string LocationId { get; set; } = string.Empty;

    [MaxLength(64)]
    public string Code { get; set; } = string.Empty;

    [MaxLength(128)]
    public string Country { get; set; } = string.Empty;

    [MaxLength(128)]
    public string City { get; set; } = string.Empty;

    [MaxLength(256)]
    public string DisplayName { get; set; } = string.Empty;

    public bool Enabled { get; set; } = true;

    public int SortOrder { get; set; }

    public DateTime CreatedAt { get; set; }

    public DateTime UpdatedAt { get; set; }

    [MaxLength(8)]
    public string? CountryCode { get; set; }
}
