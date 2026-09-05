namespace Nyxveil.ControlPlane.Application.Options;

public sealed class SigningOptions
{
    public const string SectionName = "Signing";

    /// <summary>JWT issuer claim (e.g. nyxveil-control-plane).</summary>
    public string Issuer { get; set; } = "nyxveil-control-plane";

    /// <summary>JWT audience claim — must be exactly <c>nvp-node</c> for Frozen Core NVP/1.</summary>
    public string Audience { get; set; } = "nvp-node";

    /// <summary>Directory or DPAPI-protected path for Ed25519 signing key material.</summary>
    public string KeyProtectionPath { get; set; } = string.Empty;
}
