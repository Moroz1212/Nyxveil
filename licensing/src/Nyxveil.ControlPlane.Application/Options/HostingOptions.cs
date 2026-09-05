namespace Nyxveil.ControlPlane.Application.Options;

/// <summary>
/// Kestrel listen settings. <see cref="Port"/> is the single source of truth for HTTPS binding —
/// do not also configure <c>Kestrel:Endpoints</c> for listening when Hosting.Port &gt; 0.
/// </summary>
public sealed class HostingOptions
{
    public const string SectionName = "Hosting";

    /// <summary>Default HTTPS port (installer suggestion). Any free port in 1..65535 is valid.</summary>
    public const int DefaultPort = 8443;

    /// <summary>Listen address for Kestrel (e.g. 0.0.0.0 or 127.0.0.1).</summary>
    public string BindAddress { get; set; } = "0.0.0.0";

    /// <summary>HTTPS listen port. Installer prompt default is 8443; any free TCP port is valid.</summary>
    public int Port { get; set; } = DefaultPort;

    /// <summary>Hostname used for certificate CN/SAN and TLS client SNI validation.</summary>
    public string PublicHostname { get; set; } = "localhost";

    /// <summary>Public base URL advertised to operators (e.g. https://control.example.com:8443).</summary>
    public string PublicBaseUrl { get; set; } = string.Empty;

    public static bool IsValidPort(int port) => port is >= 1 and <= 65535;
}
