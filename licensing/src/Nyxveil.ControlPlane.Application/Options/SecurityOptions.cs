namespace Nyxveil.ControlPlane.Application.Options;

/// <summary>
/// Security-sensitive settings. Never log <see cref="LicenseKekHex"/>.
/// </summary>
public sealed class SecurityOptions
{
    public const string SectionName = "Security";

    /// <summary>
    /// 64 hex characters (32 bytes) HMAC key for license secret verifiers (hmac1:...).
    /// Must never appear in logs or ToString output.
    /// </summary>
    public string LicenseKekHex { get; set; } = string.Empty;

    public override string ToString() =>
        $"{nameof(SecurityOptions)} {{ {nameof(LicenseKekHex)} = [REDACTED] }}";
}
