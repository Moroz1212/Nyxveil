using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

/// <summary>Pre-change certificate gate. Fails closed before any live mutation.</summary>
public static class TlsCertificateGate
{
    public const string ServerAuthOid = "1.3.6.1.5.5.7.3.1";

    public static void ValidateForPublicHttps(
        X509Certificate2 certificate,
        string expectedHostname,
        bool requireSystemTrust = true,
        DateTimeOffset? utcNow = null,
        Func<X509Certificate2, (bool Ok, string Error)>? chainTrustOverride = null)
    {
        ArgumentNullException.ThrowIfNull(certificate);
        ArgumentException.ThrowIfNullOrWhiteSpace(expectedHostname);

        if (!certificate.HasPrivateKey)
            throw new InvalidOperationException("Certificate private key is missing or inaccessible.");

        using (var rsa = certificate.GetRSAPrivateKey())
        using (var ecdsa = certificate.GetECDsaPrivateKey())
        {
            if (rsa is null && ecdsa is null)
                throw new InvalidOperationException("Certificate private key type is not supported (need RSA or ECDSA).");
        }

        var now = utcNow ?? DateTimeOffset.UtcNow;
        if (certificate.NotBefore.ToUniversalTime() > now.UtcDateTime)
            throw new InvalidOperationException("Certificate is not yet valid (NotBefore in the future).");
        if (certificate.NotAfter.ToUniversalTime() < now.UtcDateTime)
            throw new InvalidOperationException("Certificate is expired.");

        var host = CertificateHostnameValidator.NormalizeHost(expectedHostname);
        if (!certificate.MatchesHostname(host))
        {
            throw new InvalidOperationException(
                $"Certificate SAN/CN does not contain exact hostname '{host}'.");
        }

        if (!HasServerAuthenticationEku(certificate))
            throw new InvalidOperationException("Certificate lacks Server Authentication EKU (1.3.6.1.5.5.7.3.1).");

        if (IsSelfSigned(certificate))
            throw new InvalidOperationException(
                "Self-signed certificates are rejected for SystemTrust public HTTPS reconfiguration.");

        if (!requireSystemTrust)
            return;

        if (chainTrustOverride is not null)
        {
            var (ok, error) = chainTrustOverride(certificate);
            if (!ok)
                throw new InvalidOperationException(error);
            return;
        }

        var (trusted, trustError) = EvaluateSystemTrust(certificate, host);
        if (!trusted)
            throw new InvalidOperationException(trustError);
    }

    public static bool HasServerAuthenticationEku(X509Certificate2 certificate)
    {
        var eku = certificate.Extensions.OfType<X509EnhancedKeyUsageExtension>().FirstOrDefault();
        if (eku is null)
            return false;

        return eku.EnhancedKeyUsages.Cast<Oid>().Any(o => o.Value == ServerAuthOid);
    }

    public static bool IsSelfSigned(X509Certificate2 certificate) =>
        string.Equals(certificate.Subject, certificate.Issuer, StringComparison.OrdinalIgnoreCase);

    public static (bool Ok, string Error) EvaluateSystemTrust(X509Certificate2 certificate, string hostname)
    {
        using var chain = new X509Chain();
        chain.ChainPolicy.RevocationMode = X509RevocationMode.Online;
        chain.ChainPolicy.RevocationFlag = X509RevocationFlag.ExcludeRoot;
        chain.ChainPolicy.VerificationFlags = X509VerificationFlags.NoFlag;
        chain.ChainPolicy.UrlRetrievalTimeout = TimeSpan.FromSeconds(15);

        var built = chain.Build(certificate);
        if (!built)
        {
            var statuses = string.Join("; ",
                chain.ChainStatus.Select(s => $"{s.Status}: {s.StatusInformation}".Trim()));
            return (false, "Windows SystemTrust chain validation failed: " + statuses);
        }

        _ = hostname;
        return (true, "SystemTrust OK");
    }
}
