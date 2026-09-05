namespace Nyxveil.ControlPlane.Application.Options;

public sealed class SetupOptions
{
    public const string SectionName = "Setup";

    /// <summary>
    /// When false (Production default), anonymous web SuperAdmin bootstrap is disabled.
    /// Prefer installer CLI: <c>admin create</c>.
    /// </summary>
    public bool AllowWebBootstrap { get; set; }

    /// <summary>
    /// High-entropy one-time token required for Production web bootstrap (header or form).
    /// Compared in constant time; empty disables Production web bootstrap even if allowed.
    /// </summary>
    public string BootstrapToken { get; set; } = string.Empty;
}
