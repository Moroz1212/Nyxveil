using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class Device
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string ClientDeviceId { get; set; } = string.Empty;

    public Guid LicenseId { get; set; }

    public byte[] PublicKey { get; set; } = Array.Empty<byte>();

    [MaxLength(64)]
    public string? Platform { get; set; }

    [MaxLength(256)]
    public string? DeviceName { get; set; }

    public DeviceStatus Status { get; set; } = DeviceStatus.Active;

    public DateTime CreatedAt { get; set; }

    public DateTime? LastSeenAt { get; set; }

    public DateTime? RevokedAt { get; set; }

    public License License { get; set; } = null!;
}
