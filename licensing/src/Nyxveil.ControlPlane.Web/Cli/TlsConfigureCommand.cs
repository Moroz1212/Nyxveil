using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Production-safe TLS reconfigure for an existing Control Plane install.
/// Usage:
///   tls configure --hostname cp.nyxveil.ru --public-url https://cp.nyxveil.ru:18443 --certificate-thumbprint HEX [--check]
///   tls configure ... --certificate-pfx PATH [--certificate-pfx-password PASS]
///   tls status [--install-dir PATH]
/// </summary>
public static class TlsConfigureCommand
{
    public static int Run(string[] args)
    {
        try
        {
            if (args.Length < 2)
            {
                PrintUsage();
                return 1;
            }

            var sub = args[1];
            if (sub.Equals("status", StringComparison.OrdinalIgnoreCase))
                return RunStatus(args);
            if (sub.Equals("configure", StringComparison.OrdinalIgnoreCase))
                return RunConfigure(args);

            PrintUsage();
            return 1;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine("FAIL: " + ex.Message);
            return 1;
        }
    }

    private static int RunStatus(string[] args)
    {
        var installDir = @"C:\Program Files\Nyxveil\ControlPlane";
        var account = @"NT SERVICE\NyxveilControlPlane";
        for (var i = 2; i < args.Length; i++)
        {
            if (args[i].Equals("--install-dir", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
                installDir = args[++i];
            else if (args[i].Equals("--service-account", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
                account = args[++i];
            else
                throw new InvalidOperationException("Unknown argument: " + args[i]);
        }

        // Prefer InstallDir from ProgramData operational.json when present.
        try
        {
            var snap = TlsConfigureEngine.CaptureSnapshot(installDir);
            _ = snap;
        }
        catch
        {
            if (File.Exists(TlsConfigPaths.OperationalProgramData))
            {
                using var doc = JsonDocument.Parse(File.ReadAllText(TlsConfigPaths.OperationalProgramData));
                if (doc.RootElement.TryGetProperty("InstallDir", out var id) &&
                    id.ValueKind == JsonValueKind.String &&
                    !string.IsNullOrWhiteSpace(id.GetString()))
                {
                    installDir = id.GetString()!;
                }
            }
        }

        var engine = new TlsConfigureEngine(
            OperatingSystem.IsWindows() ? new WindowsTlsServiceController() : new RecordingTlsServiceController(),
            OperatingSystem.IsWindows() ? new WindowsTlsPrivateKeyAcl() : new RecordingTlsPrivateKeyAcl());

        var report = engine.Status(installDir, account);
        Console.WriteLine("local_listen_port=" + report.LocalListenPort);
        Console.WriteLine("PublicHostname=" + report.PublicHostname);
        Console.WriteLine("PublicBaseUrl=" + report.PublicBaseUrl);
        Console.WriteLine("CertificateMode=" + report.CertificateMode);
        Console.WriteLine("ValidationMode=" + report.ValidationMode);
        Console.WriteLine("thumbprint=" + report.Thumbprint);
        Console.WriteLine("subject=" + report.Subject);
        Console.WriteLine("SAN=" + report.San);
        Console.WriteLine("issuer=" + report.Issuer);
        Console.WriteLine("not_before=" + report.NotBefore);
        Console.WriteLine("not_after=" + report.NotAfter);
        Console.WriteLine("has_private_key=" + report.HasPrivateKey);
        Console.WriteLine("public_trust=" + report.PublicTrustResult);
        Console.WriteLine("service_account_private_key_access=" + report.ServiceAccountPrivateKeyAccess);
        Console.WriteLine("note=" + report.PublicBaseUrlNote);
        return 0;
    }

    private static int RunConfigure(string[] args)
    {
        var check = false;
        string? hostname = null;
        string? publicUrl = null;
        string? thumb = null;
        string? pfx = null;
        string? pfxPassword = null;
        var installDir = @"C:\Program Files\Nyxveil\ControlPlane";
        var serviceName = "NyxveilControlPlane";
        var serviceAccount = @"NT SERVICE\NyxveilControlPlane";

        for (var i = 2; i < args.Length; i++)
        {
            var a = args[i];
            if (a.Equals("--check", StringComparison.OrdinalIgnoreCase) ||
                a.Equals("--dry-run", StringComparison.OrdinalIgnoreCase))
            {
                check = true;
                continue;
            }

            if (a.Equals("--hostname", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                hostname = args[++i];
                continue;
            }

            if (a.Equals("--public-url", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                publicUrl = args[++i];
                continue;
            }

            if (a.Equals("--certificate-thumbprint", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                thumb = args[++i];
                continue;
            }

            if (a.Equals("--certificate-pfx", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                pfx = args[++i];
                continue;
            }

            if (a.Equals("--certificate-pfx-password", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                pfxPassword = args[++i];
                continue;
            }

            if (a.Equals("--install-dir", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                installDir = args[++i];
                continue;
            }

            if (a.Equals("--service-name", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                serviceName = args[++i];
                continue;
            }

            if (a.Equals("--service-account", StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                serviceAccount = args[++i];
                continue;
            }

            throw new InvalidOperationException("Unknown argument: " + a);
        }

        if (string.IsNullOrWhiteSpace(hostname) || string.IsNullOrWhiteSpace(publicUrl))
        {
            PrintUsage();
            return 1;
        }

        // Resolve InstallDir from operational.json when default and file exists.
        if (string.Equals(installDir, @"C:\Program Files\Nyxveil\ControlPlane", StringComparison.OrdinalIgnoreCase) &&
            File.Exists(TlsConfigPaths.OperationalProgramData))
        {
            using var doc = JsonDocument.Parse(File.ReadAllText(TlsConfigPaths.OperationalProgramData));
            if (doc.RootElement.TryGetProperty("InstallDir", out var id) &&
                id.ValueKind == JsonValueKind.String &&
                !string.IsNullOrWhiteSpace(id.GetString()))
            {
                installDir = id.GetString()!;
            }
        }

        var request = new TlsConfigureRequest
        {
            InstallDir = installDir,
            Hostname = hostname!,
            PublicUrl = publicUrl!,
            CertificateThumbprint = thumb,
            CertificatePfxPath = pfx,
            CertificatePfxPassword = pfxPassword,
            ServiceName = serviceName,
            ServiceAccount = serviceAccount,
            CheckOnly = check
        };

        Console.WriteLine(PublicBaseUrlSemantics.Documentation);

        var engine = TlsConfigureEngine.CreateDefault();
        var result = engine.Configure(request);

        foreach (var line in result.IntendedChanges)
            Console.WriteLine("plan: " + line);

        Console.WriteLine("dry_run=" + result.DryRun);
        Console.WriteLine("success=" + result.Success);
        Console.WriteLine("rolled_back=" + result.RolledBack);
        Console.WriteLine("preserved_port=" + result.PreservedPort);
        Console.WriteLine("previous_thumbprint=" + result.PreviousThumbprint);
        Console.WriteLine("new_thumbprint=" + result.NewThumbprint);
        Console.WriteLine("previous_hostname=" + result.PreviousHostname);
        Console.WriteLine("new_hostname=" + result.NewHostname);
        Console.WriteLine("previous_public_base_url=" + result.PreviousPublicBaseUrl);
        Console.WriteLine("new_public_base_url=" + result.NewPublicBaseUrl);
        Console.WriteLine("validation_mode=" + result.NewValidationMode);
        Console.WriteLine("message=" + result.Message);

        if (!result.Success)
            return 1;
        if (result.RolledBack)
            return 1;
        return 0;
    }

    private static void PrintUsage()
    {
        Console.Error.WriteLine(
            """
            Usage:
              tls configure --hostname <host> --public-url https://host:18443 --certificate-thumbprint <hex> [--check]
              tls configure --hostname <host> --public-url https://host:18443 --certificate-pfx <path> [--certificate-pfx-password <pass>] [--check]
              tls status [--install-dir <path>]

            Notes:
              - Does not change local listen port (keeps existing Hosting:Port, typically 8443).
              - Does not bind 80/443/18443.
              - PublicBaseUrl is the external advertised URL (NAT port), not the Kestrel port.
              - Old certificates are never deleted.
            """);
    }
}
