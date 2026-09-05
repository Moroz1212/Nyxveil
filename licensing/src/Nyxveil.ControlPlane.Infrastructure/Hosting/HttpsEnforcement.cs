using System.Security.Cryptography.X509Certificates;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting;

/// <summary>
/// Production HTTPS fail-closed checks. An https:// URL alone is never sufficient —
/// a certificate with a usable private key must be loadable.
/// </summary>
public static class HttpsEnforcement
{
    public static void EnforceProductionHttps(
        IHostEnvironment environment,
        IConfiguration configuration,
        X509Certificate2? loadedCertificate = null)
    {
        if (!environment.IsProduction())
            return;

        var require = configuration.GetValue($"{HttpsOptions.SectionName}:RequireHttpsInProduction", true);
        if (!require)
            return;

        if (loadedCertificate is not null && loadedCertificate.HasPrivateKey)
            return;

        var certOptions = CertificateLoader.Bind(configuration);
        var hosting = configuration.GetSection(HostingOptions.SectionName).Get<HostingOptions>()
                      ?? new HostingOptions();

        if (CertificateLoader.TryLoad(
                certOptions,
                hosting.PublicHostname,
                isProduction: true,
                out var cert,
                out var error))
        {
            cert?.Dispose();
            return;
        }

        throw new InvalidOperationException(
            "HTTPS is required in Production (Https:RequireHttpsInProduction=true) but no usable " +
            "certificate with a private key could be loaded. " +
            "Prefer Certificate:Mode=Store with a valid LocalMachine\\My thumbprint after install. " +
            "An https:// URL without a certificate is not accepted. " +
            (error is null ? string.Empty : "Detail: " + error));
    }
}
