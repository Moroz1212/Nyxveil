using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography.X509Certificates;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting;

/// <summary>
/// Local HTTPS probe used by CLI self-test. Never TrustAll —
/// SystemTrust uses full SslStream validation; SelfSignedPinned pins thumbprint+hostname+validity.
/// </summary>
public static class LocalTlsHealth
{
    /// <summary>
    /// TLS TargetHost / SNI hostname — same semantics as Deploy.psm1 Get-HealthTarget.PublicHostname.
    /// </summary>
    public static string ResolveProbeHostname(HostingOptions? hosting)
    {
        var hostName = hosting?.PublicHostname?.Trim();
        if (string.IsNullOrWhiteSpace(hostName))
            return "localhost";

        if (hostName.StartsWith("http://", StringComparison.OrdinalIgnoreCase) ||
            hostName.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
        {
            if (Uri.TryCreate(hostName, UriKind.Absolute, out var uri) && !string.IsNullOrWhiteSpace(uri.Host))
                return uri.Host;
        }

        return hostName;
    }

    /// <summary>
    /// Resolves validation mode: explicit <see cref="CertificateOptions.ValidationMode"/>, else
    /// SelfSignedPinned for Mode=SelfSigned, otherwise SystemTrust (Store/PFX default).
    /// </summary>
    public static CertificateValidationMode ResolveValidationMode(CertificateOptions options)
    {
        ArgumentNullException.ThrowIfNull(options);

        if (options.ValidationMode is { } explicitMode)
            return explicitMode;

        return options.Mode == CertificateMode.SelfSigned
            ? CertificateValidationMode.SelfSignedPinned
            : CertificateValidationMode.SystemTrust;
    }

    /// <summary>
    /// Validates that <paramref name="certificate"/> matches <paramref name="expectedHostname"/> via CN/SAN
    /// (same intent as TLS hostname checks — never TrustAll).
    /// </summary>
    public static bool ValidateCertificateForHostname(X509Certificate2 certificate, string expectedHostname)
    {
        ArgumentNullException.ThrowIfNull(certificate);
        ArgumentException.ThrowIfNullOrWhiteSpace(expectedHostname);

        var hostname = ResolveProbeHostname(new HostingOptions { PublicHostname = expectedHostname });
        return certificate.MatchesHostname(hostname);
    }

    /// <summary>
    /// Builds client SSL options for the local loopback probe.
    /// SystemTrust: no custom callback (full chain + hostname via SslStream).
    /// SelfSignedPinned: callback requires thumbprint + hostname + validity (never TrustAll).
    /// </summary>
    public static SslClientAuthenticationOptions CreateProbeSslOptions(
        string hostname,
        CertificateValidationMode validationMode,
        string? expectedThumbprint)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(hostname);

        var sslOptions = new SslClientAuthenticationOptions
        {
            TargetHost = hostname,
            EnabledSslProtocols = SslProtocols.Tls12 | SslProtocols.Tls13
        };

        if (validationMode == CertificateValidationMode.SystemTrust)
        {
            // Do NOT set RemoteCertificateValidationCallback — SslStream performs full validation.
            return sslOptions;
        }

        var pin = expectedThumbprint
                  ?? throw new InvalidOperationException(
                      "SelfSignedPinned TLS probe requires Certificate:Thumbprint for pinning (never TrustAll).");

        sslOptions.RemoteCertificateValidationCallback = (_, cert, _, _) =>
        {
            if (cert is null)
                return false;

            using var presented = new X509Certificate2(cert);
            return CertificateHostnameValidator.ValidatePinnedCertificate(presented, hostname, pin);
        };

        return sslOptions;
    }

    public static async Task<(bool Ok, string Message)> ProbeAsync(
        IConfiguration configuration,
        CancellationToken cancellationToken = default)
    {
        var hosting = configuration.GetSection(HostingOptions.SectionName).Get<HostingOptions>()
                      ?? new HostingOptions();
        var certOptions = CertificateLoader.Bind(configuration);
        var validationMode = ResolveValidationMode(certOptions);

        var port = hosting.Port > 0 ? hosting.Port : HostingOptions.DefaultPort;
        var hostname = ResolveProbeHostname(hosting);

        string? expectedThumbprint = null;
        if (!string.IsNullOrWhiteSpace(certOptions.Thumbprint))
        {
            expectedThumbprint = CertificateLoader.NormalizeThumbprint(certOptions.Thumbprint);
        }
        else if (validationMode == CertificateValidationMode.SelfSignedPinned &&
                 CertificateLoader.TryLoad(certOptions, hostname, out var loaded, out _) &&
                 loaded is not null)
        {
            expectedThumbprint = CertificateLoader.NormalizeThumbprint(loaded.Thumbprint);
            loaded.Dispose();
        }

        try
        {
            using var client = new TcpClient();
            await client.ConnectAsync(IPAddress.Loopback, port, cancellationToken).ConfigureAwait(false);
            await using var network = client.GetStream();

            var sslOptions = CreateProbeSslOptions(hostname, validationMode, expectedThumbprint);

            await using var ssl = new SslStream(network, leaveInnerStreamOpen: false);
            await ssl.AuthenticateAsClientAsync(sslOptions, cancellationToken).ConfigureAwait(false);

            return (true, $"TLS OK to 127.0.0.1:{port} (SNI={hostname}, mode={validationMode})");
        }
        catch (Exception ex)
        {
            return (false, "TLS probe failed: " + ex.Message);
        }
    }
}
