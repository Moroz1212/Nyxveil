namespace Nyxveil.ControlPlane.Application.Options;

public sealed class TicketOptions
{
    public const string SectionName = "Tickets";

    /// <summary>Access ticket lifetime in minutes (NVP/1 default 15).</summary>
    public int TtlMinutes { get; set; } = 15;
}
