namespace Nyxveil.ControlPlane.Application.Options;

public sealed class AcmeOptions
{
    public const string SectionName = "Acme";

    /// <summary>When true, use Let's Encrypt staging (or fake provider in tests).</summary>
    public bool UseStaging { get; set; } = true;

    /// <summary>Directory under ProgramData for ACME account key (never DB).</summary>
    public string AccountKeyRelativePath { get; set; } = "secrets/acme-account.pem";

    public string? ContactEmail { get; set; }

    /// <summary>When true, skip real Certes and use in-process fake order (tests/dev).</summary>
    public bool UseFakeProvider { get; set; }
}
