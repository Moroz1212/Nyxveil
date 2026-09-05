namespace Nyxveil.ControlPlane.Application.Options;

public sealed class RateLimitOptions
{
    public const string SectionName = "RateLimits";

    /// <summary>Requests allowed per window per IP (Core default 120/min).</summary>
    public int PermitLimit { get; set; } = 120;

    /// <summary>Window length in seconds.</summary>
    public int WindowSeconds { get; set; } = 60;

    /// <summary>Burst capacity above the steady rate (Core default 30).</summary>
    public int Burst { get; set; } = 30;

    /// <summary>Separate tighter limit for ticket issue/refresh endpoints.</summary>
    public int TicketPermitLimit { get; set; } = 60;

    public int TicketWindowSeconds { get; set; } = 60;
}
