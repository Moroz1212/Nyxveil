using System.Security.Cryptography.X509Certificates;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Hosting;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Offline certificate hostname / pin validation for installer gates.
/// Usage: certificate validate --hostname X --thumbprint Y [--self-signed-pinned]
/// </summary>
public static class CertificateValidateCommand
{
    public static int Run(string[] args)
    {
        try
        {
            if (!TryParse(args, out var hostname, out var thumbprint, out var selfSignedPinned, out var error))
            {
                Console.Error.WriteLine("FAIL: " + error);
                Console.Error.WriteLine(
                    "Usage: certificate validate --hostname <host> --thumbprint <hex> [--self-signed-pinned]");
                return 1;
            }

            var options = new CertificateOptions
            {
                Mode = CertificateMode.Store,
                Thumbprint = thumbprint,
                ValidationMode = selfSignedPinned
                    ? CertificateValidationMode.SelfSignedPinned
                    : CertificateValidationMode.SystemTrust
            };

            if (!CertificateLoader.TryLoad(options, hostname, out var cert, out var loadError) || cert is null)
            {
                Console.Error.WriteLine("FAIL: certificate not loadable: " + (loadError ?? "unknown"));
                return 1;
            }

            using (cert)
            {
                if (selfSignedPinned)
                {
                    if (!CertificateHostnameValidator.ValidatePinnedCertificate(cert, hostname, thumbprint))
                    {
                        Console.Error.WriteLine(
                            "FAIL: SelfSignedPinned validation failed (thumbprint, hostname, or validity).");
                        return 1;
                    }
                }
                else if (!cert.MatchesHostname(hostname))
                {
                    // SystemTrust path for CLI offline check: hostname must match; chain is verified at TLS probe time.
                    Console.Error.WriteLine("FAIL: certificate does not match hostname '" + hostname + "'.");
                    return 1;
                }

                // Always reject expired / not-yet-valid for installer.
                var now = DateTimeOffset.UtcNow;
                if (cert.NotBefore.ToUniversalTime() > now.UtcDateTime ||
                    cert.NotAfter.ToUniversalTime() < now.UtcDateTime)
                {
                    Console.Error.WriteLine("FAIL: certificate is outside its validity window.");
                    return 1;
                }
            }

            Console.WriteLine("OK: certificate valid for " + hostname);
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine("FAIL: " + ex.Message);
            return 1;
        }
    }

    private static bool TryParse(
        string[] args,
        out string hostname,
        out string thumbprint,
        out bool selfSignedPinned,
        out string error)
    {
        hostname = "";
        thumbprint = "";
        selfSignedPinned = false;
        error = "";

        // args[0]=certificate args[1]=validate ...
        for (var i = 2; i < args.Length; i++)
        {
            var a = args[i];
            if (a.Equals("--hostname", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                hostname = args[++i];
                continue;
            }

            if (a.Equals("--thumbprint", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                thumbprint = args[++i];
                continue;
            }

            if (a.Equals("--self-signed-pinned", StringComparison.OrdinalIgnoreCase))
            {
                selfSignedPinned = true;
                continue;
            }

            error = "Unknown argument: " + a;
            return false;
        }

        if (string.IsNullOrWhiteSpace(hostname))
        {
            error = "--hostname is required";
            return false;
        }

        if (string.IsNullOrWhiteSpace(thumbprint))
        {
            error = "--thumbprint is required";
            return false;
        }

        return true;
    }
}
