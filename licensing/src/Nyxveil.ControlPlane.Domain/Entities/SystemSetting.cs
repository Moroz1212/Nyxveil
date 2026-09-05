using System.ComponentModel.DataAnnotations;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class SystemSetting
{
    [MaxLength(128)]
    public string Key { get; set; } = string.Empty;

    [MaxLength(8000)]
    public string Value { get; set; } = string.Empty;

    public DateTime UpdatedAt { get; set; }

    [MaxLength(256)]
    public string? UpdatedBy { get; set; }
}
