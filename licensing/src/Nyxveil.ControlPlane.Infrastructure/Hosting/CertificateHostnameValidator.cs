using System.Security.Cryptography.X509Certificates;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting;

/// <summary>
/// Hostname helpers for TLS probe pinning, CLI <c>certificate validate</c>, and installer gates.
/// Matching uses .NET 10 <see cref="X509Certificate2.MatchesHostname"/> (not custom wildcards).
/// </summary>
public static class CertificateHostnameValidator
{
    /// <summary>
    /// SelfSignedPinned gate: thumbprint match + hostname + not-before/not-after window.
    /// Never TrustAll.
    /// </summary>
    public static bool ValidatePinnedCertificate(
        X509Certificate2 certificate,
        string expectedHostname,
        string expectedThumbprint,
        DateTimeOffset? utcNow = null)
    {
        ArgumentNullException.ThrowIfNull(certificate);
        ArgumentException.ThrowIfNullOrWhiteSpace(expectedHostname);
        ArgumentException.ThrowIfNullOrWhiteSpace(expectedThumbprint);

        var now = utcNow ?? DateTimeOffset.UtcNow;
        if (certificate.NotBefore.ToUniversalTime() > now.UtcDateTime)
            return false;
        if (certificate.NotAfter.ToUniversalTime() < now.UtcDateTime)
            return false;

        var actual = CertificateLoader.NormalizeThumbprint(certificate.Thumbprint);
        var pin = CertificateLoader.NormalizeThumbprint(expectedThumbprint);
        if (!string.Equals(actual, pin, StringComparison.OrdinalIgnoreCase))
            return false;

        var host = NormalizeHost(expectedHostname);
        return !string.IsNullOrEmpty(host) && certificate.MatchesHostname(host);
    }

    internal static string NormalizeHost(string hostname)
    {
        var host = hostname.Trim().TrimEnd('.');
        if (host.StartsWith("http://", StringComparison.OrdinalIgnoreCase) ||
            host.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
        {
            if (Uri.TryCreate(host, UriKind.Absolute, out var uri) && !string.IsNullOrWhiteSpace(uri.Host))
                return uri.Host.TrimEnd('.');
        }

        return host;
    }
}
