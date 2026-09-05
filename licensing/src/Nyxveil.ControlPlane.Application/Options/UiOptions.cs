namespace Nyxveil.ControlPlane.Application.Options;

public sealed class UiOptions
{
    public const string SectionName = "UI";

    public string AppName { get; set; } = "Nyxveil Control Plane";

    public string? SupportUrl { get; set; }

    /// <summary>When true, admin UI shows test_only nodes by default.</summary>
    public bool ShowTestNodes { get; set; } = true;

    /// <summary>Path base for Blazor admin (e.g. /admin).</summary>
    public string PathBase { get; set; } = "/";
}
