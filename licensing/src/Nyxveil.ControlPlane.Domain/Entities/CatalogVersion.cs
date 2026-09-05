using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class CatalogVersion
{
    public Guid Id { get; set; }

    [MaxLength(64)]
    public string Version { get; set; } = string.Empty;

    public DateTime IssuedAt { get; set; }

    public DateTime ExpiresAt { get; set; }

    [MaxLength(128)]
    public string KeyId { get; set; } = string.Empty;

    [MaxLength(128)]
    public string? PayloadHash { get; set; }

    public DateTime CreatedAt { get; set; }
}
