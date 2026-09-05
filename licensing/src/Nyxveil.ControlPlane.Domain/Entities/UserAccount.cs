using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class UserAccount
{
    public Guid UserId { get; set; }

    [MaxLength(256)]
    public string? ExternalId { get; set; }

    [MaxLength(320)]
    public string? Email { get; set; }

    [MaxLength(256)]
    public string? DisplayName { get; set; }

    [MaxLength(64)]
    public string Status { get; set; } = "Active";

    public DateTime CreatedAt { get; set; }

    public DateTime UpdatedAt { get; set; }
}
