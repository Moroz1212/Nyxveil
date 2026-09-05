namespace Nyxveil.ControlPlane.Application.Options;

/// <summary>
/// How local TLS health / CLI validation trusts the presented server certificate.
/// Separate from <see cref="CertificateMode"/> (load source).
/// </summary>
public enum CertificateValidationMode
{
    /// <summary>
    /// Default for Store/PFX after CA import: full SslStream chain + hostname validation.
    /// Do not install a custom <c>RemoteCertificateValidationCallback</c>.
    /// </summary>
    SystemTrust = 0,

    /// <summary>
    /// Self-signed / lab pins: thumbprint + hostname + validity window only (never TrustAll).
    /// </summary>
    SelfSignedPinned = 1
}
