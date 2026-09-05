namespace Nyxveil.ControlPlane.Application.Options;

public enum CertificateMode
{
    /// <summary>Preferred production mode: load by thumbprint from the Windows certificate store.</summary>
    Store = 0,

    /// <summary>Load from a PFX file (prefer importing into the store, then switch to <see cref="Store"/>).</summary>
    Pfx = 1,

    /// <summary>
    /// Development / installer bootstrap. When Thumbprint is set, loads from the store (persisted cert).
    /// Runtime CreateSelfSigned is only for thumbprint-less non-production use.
    /// </summary>
    SelfSigned = 2
}

/// <summary>
/// HTTPS certificate settings. Production preferred shape after install/import:
/// <c>Mode=Store</c> + <c>Thumbprint</c> (LocalMachine\My).
/// </summary>
public sealed class CertificateOptions
{
    public const string SectionName = "Certificate";

    /// <summary>Preferred production value is <see cref="CertificateMode.Store"/>.</summary>
    public CertificateMode Mode { get; set; } = CertificateMode.Store;

    /// <summary>
    /// TLS probe / CLI trust policy. Independent of <see cref="Mode"/> (load source).
    /// Null = auto: <see cref="CertificateValidationMode.SelfSignedPinned"/> when Mode is SelfSigned,
    /// otherwise <see cref="CertificateValidationMode.SystemTrust"/> (Store/PFX default).
    /// Self-signed certs imported into the store must set SelfSignedPinned explicitly.
    /// </summary>
    public CertificateValidationMode? ValidationMode { get; set; }

    /// <summary>Certificate thumbprint (hex, spaces optional) when Mode is Store, or SelfSigned after install.</summary>
    public string Thumbprint { get; set; } = string.Empty;

    public string StoreName { get; set; } = "My";

    /// <summary>LocalMachine or CurrentUser.</summary>
    public string StoreLocation { get; set; } = "LocalMachine";

    public string PfxPath { get; set; } = string.Empty;

    /// <summary>
    /// Path to a DPAPI-protected file containing the PFX password (UTF-8 after unprotect).
    /// </summary>
    public string PfxPasswordProtectedPath { get; set; } = string.Empty;
}
