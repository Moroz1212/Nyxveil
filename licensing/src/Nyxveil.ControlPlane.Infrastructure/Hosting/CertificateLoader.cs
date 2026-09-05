using System.Net;
using System.Runtime.Versioning;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Options;
using ProtectedData = System.Security.Cryptography.ProtectedData;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting;

/// <summary>
/// Loads an X509 certificate with a private key for Kestrel HTTPS.
/// Mere presence of an https:// URL is not treated as a usable certificate.
/// Preferred production config after install/PFX import: Mode=Store + Thumbprint.
/// </summary>
public static class CertificateLoader
{
    public static bool TryLoad(
        CertificateOptions options,
        string? publicHostname,
        out X509Certificate2? certificate,
        out string? error)
    {
        return TryLoad(options, publicHostname, isProduction: false, out certificate, out error);
    }

    public static bool TryLoad(
        CertificateOptions options,
        string? publicHostname,
        bool isProduction,
        out X509Certificate2? certificate,
        out string? error)
    {
        certificate = null;
        error = null;

        try
        {
            certificate = options.Mode switch
            {
                CertificateMode.Store => LoadFromStore(options),
                CertificateMode.Pfx => LoadFromPfx(options),
                CertificateMode.SelfSigned => LoadSelfSigned(options, publicHostname, isProduction),
                _ => throw new InvalidOperationException($"Unknown certificate mode: {options.Mode}")
            };

            if (certificate is null || !certificate.HasPrivateKey)
            {
                error = "Certificate loaded but private key is not available to this process identity.";
                certificate?.Dispose();
                certificate = null;
                return false;
            }

            return true;
        }
        catch (Exception ex)
        {
            error = ex.Message;
            certificate = null;
            return false;
        }
    }

    public static X509Certificate2 LoadRequired(
        CertificateOptions options,
        string? publicHostname,
        bool isProduction = false)
    {
        if (!TryLoad(options, publicHostname, isProduction, out var cert, out var error) || cert is null)
        {
            throw new InvalidOperationException(
                "Failed to load HTTPS certificate with private key: " + (error ?? "unknown error"));
        }

        return cert;
    }

    /// <summary>
    /// SelfSigned: when Thumbprint is set, load from the certificate store (persisted across restarts).
    /// CreateSelfSigned only when no thumbprint and not Production.
    /// </summary>
    private static X509Certificate2 LoadSelfSigned(
        CertificateOptions options,
        string? publicHostname,
        bool isProduction)
    {
        if (!string.IsNullOrWhiteSpace(options.Thumbprint))
            return LoadFromStore(options);

        if (isProduction)
        {
            throw new InvalidOperationException(
                "Certificate:Mode=SelfSigned without Thumbprint is not allowed in Production. " +
                "Import/persist the certificate and set Mode=Store (preferred) or SelfSigned with Thumbprint.");
        }

        return CreateSelfSigned(publicHostname ?? "localhost");
    }

    /// <summary>Creates an ephemeral self-signed certificate (tests / lab only — not persisted).</summary>
    public static X509Certificate2 CreateSelfSigned(string hostname, int validityDays = 365)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(hostname);

        using var rsa = RSA.Create(2048);
        var request = new CertificateRequest(
            $"CN={hostname}",
            rsa,
            HashAlgorithmName.SHA256,
            RSASignaturePadding.Pkcs1);

        request.CertificateExtensions.Add(
            new X509BasicConstraintsExtension(false, false, 0, false));
        request.CertificateExtensions.Add(
            new X509KeyUsageExtension(
                X509KeyUsageFlags.DigitalSignature | X509KeyUsageFlags.KeyEncipherment,
                true));
        request.CertificateExtensions.Add(
            new X509SubjectKeyIdentifierExtension(request.PublicKey, false));

        var san = new SubjectAlternativeNameBuilder();
        san.AddDnsName(hostname);
        if (IPAddress.TryParse(hostname, out var ip))
            san.AddIpAddress(ip);
        request.CertificateExtensions.Add(san.Build());

        var cert = request.CreateSelfSigned(
            DateTimeOffset.UtcNow.AddDays(-1),
            DateTimeOffset.UtcNow.AddDays(validityDays));

        // Re-import to ensure private key is exportable/persistable for Kestrel on Windows.
        return X509CertificateLoader.LoadPkcs12(
            cert.Export(X509ContentType.Pfx),
            password: null,
            X509KeyStorageFlags.Exportable | X509KeyStorageFlags.EphemeralKeySet);
    }

    public static CertificateOptions Bind(IConfiguration configuration)
    {
        var options = new CertificateOptions();
        configuration.GetSection(CertificateOptions.SectionName).Bind(options);
        return options;
    }

    /// <summary>
    /// Preferred production config shape after PFX import into the Windows store.
    /// </summary>
    public static CertificateOptions PreferredStoreConfig(string thumbprint) =>
        new()
        {
            Mode = CertificateMode.Store,
            Thumbprint = NormalizeThumbprint(thumbprint),
            StoreName = "My",
            StoreLocation = "LocalMachine"
        };

    private static X509Certificate2 LoadFromStore(CertificateOptions options)
    {
        if (string.IsNullOrWhiteSpace(options.Thumbprint))
            throw new InvalidOperationException("Certificate:Thumbprint is required when loading from the certificate store.");

        var thumbprint = NormalizeThumbprint(options.Thumbprint);
        if (!Enum.TryParse<StoreLocation>(options.StoreLocation, ignoreCase: true, out var location))
            location = StoreLocation.LocalMachine;

        if (!Enum.TryParse<StoreName>(options.StoreName, ignoreCase: true, out var storeName))
            storeName = StoreName.My;

        using var store = new X509Store(storeName, location);
        store.Open(OpenFlags.ReadOnly);

        var found = store.Certificates.Find(X509FindType.FindByThumbprint, thumbprint, validOnly: false);
        if (found.Count == 0)
        {
            throw new InvalidOperationException(
                $"No certificate with thumbprint '{thumbprint}' found in {location}\\{storeName}.");
        }

        var cert = found[0];
        if (!cert.HasPrivateKey)
        {
            throw new InvalidOperationException(
                $"Certificate '{thumbprint}' was found but has no private key accessible to this identity.");
        }

        return cert;
    }

    private static X509Certificate2 LoadFromPfx(CertificateOptions options)
    {
        if (string.IsNullOrWhiteSpace(options.PfxPath))
            throw new InvalidOperationException("Certificate:PfxPath is required when Mode=Pfx.");

        if (!File.Exists(options.PfxPath))
            throw new FileNotFoundException("PFX file not found.", options.PfxPath);

        string? password = null;
        if (!string.IsNullOrWhiteSpace(options.PfxPasswordProtectedPath))
        {
            if (!OperatingSystem.IsWindows())
                throw new PlatformNotSupportedException("PFX password DPAPI files require Windows.");
            password = ReadProtectedPassword(options.PfxPasswordProtectedPath);
        }

        return X509CertificateLoader.LoadPkcs12FromFile(
            options.PfxPath,
            password,
            X509KeyStorageFlags.MachineKeySet | X509KeyStorageFlags.Exportable);
    }

    [SupportedOSPlatform("windows")]
    private static string? ReadProtectedPassword(string? protectedPath)
    {
        if (string.IsNullOrWhiteSpace(protectedPath))
            return null;

        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("PFX password DPAPI files require Windows.");

        if (!File.Exists(protectedPath))
            throw new FileNotFoundException("PFX password protected file not found.", protectedPath);

        var protectedBytes = File.ReadAllBytes(protectedPath);
        var plain = ProtectedData.Unprotect(protectedBytes, optionalEntropy: null, DataProtectionScope.LocalMachine);
        return System.Text.Encoding.UTF8.GetString(plain).TrimEnd('\0', '\r', '\n');
    }

    public static string NormalizeThumbprint(string thumbprint) =>
        new string(thumbprint.Where(Uri.IsHexDigit).Select(char.ToUpperInvariant).ToArray());
}
