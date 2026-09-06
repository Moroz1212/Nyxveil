using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json.Nodes;
using System.Runtime.Versioning;
using Microsoft.Extensions.Configuration;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

/// <summary>
/// Transactional TLS reconfigure for an existing Windows Control Plane install.
/// Runtime SoT: InstallDir\appsettings.Production.json (Hosting + Certificate).
/// Ops mirror: ProgramData\operational.json (+ InstallDir\config\operational.json).
/// Does not change Port, firewall, NAT, or unrelated services.
/// </summary>
public sealed class TlsConfigureEngine
{
    private readonly ITlsServiceController _services;
    private readonly ITlsPrivateKeyAcl _acl;

    public TlsConfigureEngine(ITlsServiceController services, ITlsPrivateKeyAcl acl)
    {
        _services = services;
        _acl = acl;
    }

    public static TlsConfigureEngine CreateDefault()
    {
        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("tls configure requires Windows.");
        return new TlsConfigureEngine(new WindowsTlsServiceController(), new WindowsTlsPrivateKeyAcl());
    }

    public TlsConfigureResult Configure(TlsConfigureRequest request, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(request);
        ValidateRequestFlags(request);

        TlsConfigPaths.AssertNotNestedConfigDir(request.InstallDir);
        var publicUrl = PublicBaseUrlSemantics.NormalizeExternalPublicUrl(request.PublicUrl);
        PublicBaseUrlSemantics.AssertHostnameMatchesPublicUrl(request.Hostname, publicUrl);

        var snapshot = CaptureSnapshot(request.InstallDir);
        AssertPortIsolation(snapshot.Port, request.ForbiddenListenPorts);

        var intended = BuildIntendedChanges(snapshot, request.Hostname, publicUrl);

        using var cert = ResolveAndValidateCertificate(request, out var importedThumbprint);

        if (!request.SkipPrivateKeyAcl && !request.CheckOnly)
        {
            // ACL grant is part of apply; for --check only validate accessibility to current process.
        }

        if (request.CheckOnly)
        {
            // Dry-run: do not import (thumbprint mode), do not ACL, do not write, do not stop service.
            if (!string.IsNullOrWhiteSpace(request.CertificatePfxPath) && string.IsNullOrWhiteSpace(importedThumbprint))
            {
                // PFX validated in-memory only; not imported.
            }

            if (!request.SkipPrivateKeyAcl &&
                string.IsNullOrWhiteSpace(request.CertificatePfxPath) &&
                !string.IsNullOrWhiteSpace(request.CertificateThumbprint))
            {
                if (!_acl.HasReadAccess(cert, request.ServiceAccount))
                {
                    // Warn in intended changes — still fail closed for check if we can detect.
                    throw new InvalidOperationException(
                        $"Service account '{request.ServiceAccount}' does not currently have private-key Read access.");
                }
            }

            return new TlsConfigureResult
            {
                DryRun = true,
                Success = true,
                PreservedPort = snapshot.Port,
                PreviousThumbprint = snapshot.CertificateThumbprint,
                NewThumbprint = CertificateLoader.NormalizeThumbprint(cert.Thumbprint),
                PreviousHostname = snapshot.PublicHostname,
                NewHostname = request.Hostname,
                PreviousPublicBaseUrl = snapshot.PublicBaseUrl,
                NewPublicBaseUrl = publicUrl,
                PreviousValidationMode = snapshot.CertificateValidationMode,
                NewValidationMode = "SystemTrust",
                IntendedChanges = intended,
                Message = "dry-run OK: certificate validated; Port preserved; no changes applied"
            };
        }

        // Apply path
        var newThumb = CertificateLoader.NormalizeThumbprint(
            string.IsNullOrWhiteSpace(importedThumbprint) ? cert.Thumbprint : importedThumbprint);

        X509Certificate2? storeCert = cert;
        var disposeStoreCert = false;
        try
        {
            if (!string.IsNullOrWhiteSpace(request.CertificatePfxPath))
            {
                if (request.SkipPfxStoreImport)
                {
                    newThumb = CertificateLoader.NormalizeThumbprint(cert.Thumbprint);
                    storeCert = cert;
                    disposeStoreCert = false;
                }
                else
                {
                    storeCert = ImportPfxToLocalMachineMy(request.CertificatePfxPath!, request.CertificatePfxPassword);
                    disposeStoreCert = true;
                    newThumb = CertificateLoader.NormalizeThumbprint(storeCert.Thumbprint);
                    TlsCertificateGate.ValidateForPublicHttps(
                        storeCert, request.Hostname, request.RequireSystemTrust);
                }
            }

            if (!request.SkipPrivateKeyAcl)
            {
                _acl.GrantRead(storeCert, request.ServiceAccount);
                if (!_acl.HasReadAccess(storeCert, request.ServiceAccount))
                    throw new InvalidOperationException("Private-key ACL validation failed after grant.");
                if (OperatingSystem.IsWindows() &&
                    _acl is WindowsTlsPrivateKeyAcl &&
                    WindowsTlsPrivateKeyAcl.HasBroadIdentityAce(storeCert))
                {
                    throw new InvalidOperationException(
                        "Refusing commit: private key ACL contains Everyone/Users Allow ACE.");
                }
            }

            if (!request.SkipServiceControl)
                _services.Stop(request.ServiceName);

            try
            {
                WriteConfigs(snapshot, request, publicUrl, newThumb);

                if (!request.SkipServiceControl)
                    _services.Start(request.ServiceName);

                var health = WaitHealthy(request, cancellationToken);
                if (!health.Ok)
                    throw new InvalidOperationException(health.Message);

                return new TlsConfigureResult
                {
                    DryRun = false,
                    Success = true,
                    PreservedPort = snapshot.Port,
                    PreviousThumbprint = snapshot.CertificateThumbprint,
                    NewThumbprint = newThumb,
                    PreviousHostname = snapshot.PublicHostname,
                    NewHostname = request.Hostname,
                    PreviousPublicBaseUrl = snapshot.PublicBaseUrl,
                    NewPublicBaseUrl = publicUrl,
                    PreviousValidationMode = snapshot.CertificateValidationMode,
                    NewValidationMode = "SystemTrust",
                    IntendedChanges = intended,
                    Message = "tls configure committed"
                };
            }
            catch (Exception applyEx)
            {
                RestoreSnapshot(snapshot);
                if (!request.SkipServiceControl)
                {
                    try { _services.Start(request.ServiceName); }
                    catch { /* best-effort; reported below */ }
                }

                var (ok, msg) = WaitHealthy(request, cancellationToken);
                return new TlsConfigureResult
                {
                    DryRun = false,
                    Success = false,
                    RolledBack = true,
                    PreservedPort = snapshot.Port,
                    PreviousThumbprint = snapshot.CertificateThumbprint,
                    NewThumbprint = newThumb,
                    PreviousHostname = snapshot.PublicHostname,
                    NewHostname = request.Hostname,
                    PreviousPublicBaseUrl = snapshot.PublicBaseUrl,
                    NewPublicBaseUrl = publicUrl,
                    IntendedChanges = intended,
                    Message = ok
                        ? $"rolled back to previous healthy CP after failure: {applyEx.Message}"
                        : $"rollback restored config but health still failing: {msg}; original: {applyEx.Message}"
                };
            }
        }
        finally
        {
            if (disposeStoreCert)
                storeCert?.Dispose();
        }
    }

    public TlsStatusReport Status(string installDir, string? serviceAccount = null)
    {
        TlsConfigPaths.AssertNotNestedConfigDir(installDir);
        var snapshot = CaptureSnapshot(installDir);
        serviceAccount ??= @"NT SERVICE\NyxveilControlPlane";

        var report = new TlsStatusReport
        {
            LocalListenPort = snapshot.Port,
            PublicHostname = snapshot.PublicHostname,
            PublicBaseUrl = snapshot.PublicBaseUrl,
            CertificateMode = snapshot.CertificateMode,
            ValidationMode = snapshot.CertificateValidationMode,
            Thumbprint = snapshot.CertificateThumbprint
        };

        if (string.IsNullOrWhiteSpace(snapshot.CertificateThumbprint))
        {
            return report with
            {
                PublicTrustResult = "no thumbprint configured",
                ServiceAccountPrivateKeyAccess = "n/a"
            };
        }

        var opts = CertificateLoader.PreferredStoreConfig(snapshot.CertificateThumbprint);
        if (!CertificateLoader.TryLoad(opts, snapshot.PublicHostname, out var cert, out var err) || cert is null)
        {
            return report with
            {
                HasPrivateKey = false,
                PublicTrustResult = "load failed: " + (err ?? "unknown"),
                ServiceAccountPrivateKeyAccess = "unknown"
            };
        }

        using (cert)
        {
            var sans = GetSanDisplay(cert);
            var trust = TlsCertificateGate.EvaluateSystemTrust(cert, snapshot.PublicHostname);
            var acl = "unknown";
            try
            {
                acl = _acl.HasReadAccess(cert, serviceAccount) ? "Read OK" : "MISSING Read";
            }
            catch (Exception ex)
            {
                acl = "error: " + ex.Message;
            }

            return report with
            {
                Subject = cert.Subject,
                San = sans,
                Issuer = cert.Issuer,
                NotBefore = cert.NotBefore.ToUniversalTime(),
                NotAfter = cert.NotAfter.ToUniversalTime(),
                HasPrivateKey = cert.HasPrivateKey,
                PublicTrustResult = trust.Ok ? trust.Error : trust.Error,
                ServiceAccountPrivateKeyAccess = acl
            };
        }
    }

    public static TlsConfigureSnapshot CaptureSnapshot(string installDir)
    {
        var appPath = TlsConfigPaths.AppsettingsProduction(installDir);
        if (!File.Exists(appPath))
            throw new FileNotFoundException("Missing appsettings.Production.json (runtime TLS SoT).", appPath);

        var appJson = File.ReadAllText(appPath);
        var app = TlsJsonConfig.LoadObject(appPath);

        var opPath = TlsConfigPaths.OperationalProgramData;
        string opJson;
        JsonObject op;
        if (File.Exists(opPath))
        {
            opJson = File.ReadAllText(opPath);
            op = TlsJsonConfig.LoadObject(opPath);
        }
        else
        {
            // Synthesize from appsettings when operational missing (lab only).
            opJson = "{}";
            op = new JsonObject();
        }

        var installCopy = TlsConfigPaths.OperationalInstallCopy(installDir);
        string? installCopyJson = File.Exists(installCopy) ? File.ReadAllText(installCopy) : null;

        var port = TlsJsonConfig.ReadPort(app);
        if (port <= 0)
            port = TlsJsonConfig.ReadPort(op);
        if (port <= 0)
            port = HostingOptionsDefaultPort;

        return new TlsConfigureSnapshot
        {
            AppsettingsProductionPath = appPath,
            OperationalProgramDataPath = opPath,
            OperationalInstallCopyPath = File.Exists(installCopy) ? installCopy : null,
            AppsettingsJson = appJson,
            OperationalJson = opJson,
            OperationalInstallCopyJson = installCopyJson,
            Port = port,
            PublicHostname = FirstNonEmpty(
                TlsJsonConfig.ReadString(app, "Hosting", "PublicHostname"),
                TlsJsonConfig.ReadString(op, "PublicHostname")),
            PublicBaseUrl = FirstNonEmpty(
                TlsJsonConfig.ReadString(app, "Hosting", "PublicBaseUrl"),
                TlsJsonConfig.ReadString(op, "PublicBaseUrl")),
            CertificateMode = FirstNonEmpty(
                TlsJsonConfig.ReadString(app, "Certificate", "Mode"),
                TlsJsonConfig.ReadString(op, "CertificateMode"),
                "Store"),
            CertificateValidationMode = FirstNonEmpty(
                TlsJsonConfig.ReadString(app, "Certificate", "ValidationMode"),
                TlsJsonConfig.ReadString(op, "CertificateValidationMode"),
                "SelfSignedPinned"),
            CertificateThumbprint = FirstNonEmpty(
                TlsJsonConfig.ReadString(app, "Certificate", "Thumbprint"),
                TlsJsonConfig.ReadString(op, "CertificateThumbprint")),
            FirewallRuleName = TlsJsonConfig.ReadString(op, "FirewallRuleName"),
            ServiceName = FirstNonEmpty(TlsJsonConfig.ReadString(op, "ServiceName"), "NyxveilControlPlane")
        };
    }

    private const int HostingOptionsDefaultPort = 8443;

    private static void ValidateRequestFlags(TlsConfigureRequest request)
    {
        if (string.IsNullOrWhiteSpace(request.Hostname))
            throw new InvalidOperationException("--hostname is required.");
        if (string.IsNullOrWhiteSpace(request.PublicUrl))
            throw new InvalidOperationException("--public-url is required.");

        var hasThumb = !string.IsNullOrWhiteSpace(request.CertificateThumbprint);
        var hasPfx = !string.IsNullOrWhiteSpace(request.CertificatePfxPath);
        if (hasThumb == hasPfx)
            throw new InvalidOperationException("Specify exactly one of --certificate-thumbprint or --certificate-pfx.");
    }

    public static void AssertPortIsolation(int listenPort, IEnumerable<int> forbidden)
    {
        if (listenPort is < 1 or > 65535)
            throw new InvalidOperationException($"Invalid listen port {listenPort}.");

        foreach (var f in forbidden)
        {
            if (listenPort == f)
            {
                throw new InvalidOperationException(
                    $"Refusing TLS configure: listen port must not be {f}. Local CP must remain on its existing port (typically 8443).");
            }
        }
    }

    private static IReadOnlyList<string> BuildIntendedChanges(
        TlsConfigureSnapshot snapshot, string hostname, string publicUrl) =>
        new[]
        {
            $"preserve Port={snapshot.Port}",
            $"preserve FirewallRuleName={snapshot.FirewallRuleName}",
            $"PublicHostname: {snapshot.PublicHostname} -> {hostname}",
            $"PublicBaseUrl: {snapshot.PublicBaseUrl} -> {publicUrl}",
            $"CertificateMode: {snapshot.CertificateMode} -> Store",
            $"CertificateValidationMode: {snapshot.CertificateValidationMode} -> SystemTrust",
            $"CertificateThumbprint: {snapshot.CertificateThumbprint} -> <new>",
            "no bind on 80/443/18443",
            "restart only " + snapshot.ServiceName
        };

    private X509Certificate2 ResolveAndValidateCertificate(TlsConfigureRequest request, out string? importedThumbprint)
    {
        importedThumbprint = null;
        if (!string.IsNullOrWhiteSpace(request.CertificateThumbprint))
        {
            var opts = CertificateLoader.PreferredStoreConfig(request.CertificateThumbprint!);
            if (!CertificateLoader.TryLoad(opts, request.Hostname, out var cert, out var err) || cert is null)
                throw new InvalidOperationException("Cannot load certificate from store: " + (err ?? "unknown"));

            TlsCertificateGate.ValidateForPublicHttps(cert, request.Hostname, request.RequireSystemTrust);
            return cert;
        }

        // PFX: validate in-memory first (check and apply). Import only on apply.
        var password = request.CertificatePfxPassword;
        var flags = X509KeyStorageFlags.Exportable | X509KeyStorageFlags.EphemeralKeySet;
        var cert2 = X509CertificateLoader.LoadPkcs12FromFile(request.CertificatePfxPath!, password, flags);
        try
        {
            TlsCertificateGate.ValidateForPublicHttps(cert2, request.Hostname, request.RequireSystemTrust);
        }
        catch
        {
            cert2.Dispose();
            throw;
        }

        return cert2;
    }

    private static X509Certificate2 ImportPfxToLocalMachineMy(string pfxPath, string? password)
    {
        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("PFX import to LocalMachine\\My requires Windows.");

        var cert = X509CertificateLoader.LoadPkcs12FromFile(
            pfxPath,
            password,
            X509KeyStorageFlags.MachineKeySet | X509KeyStorageFlags.PersistKeySet | X509KeyStorageFlags.Exportable);

        using var store = new X509Store(StoreName.My, StoreLocation.LocalMachine);
        store.Open(OpenFlags.ReadWrite);
        store.Add(cert);
        return cert;
    }

    private void WriteConfigs(
        TlsConfigureSnapshot snapshot,
        TlsConfigureRequest request,
        string publicUrl,
        string newThumb)
    {
        var app = JsonNode.Parse(snapshot.AppsettingsJson) as JsonObject
                  ?? throw new InvalidOperationException("Invalid appsettings snapshot.");
        var hosting = app["Hosting"] as JsonObject ?? new JsonObject();
        app["Hosting"] = hosting;

        // Preserve listen port & bind; never rewrite Port.
        if (hosting["Port"] is null)
            hosting["Port"] = snapshot.Port;
        var portVal = hosting["Port"]!.GetValue<int>();
        if (portVal != snapshot.Port)
            throw new InvalidOperationException("Internal error: attempted Port mutation.");

        hosting["PublicHostname"] = request.Hostname;
        hosting["PublicBaseUrl"] = publicUrl;

        var cert = app["Certificate"] as JsonObject ?? new JsonObject();
        app["Certificate"] = cert;
        cert["Mode"] = "Store";
        cert["ValidationMode"] = "SystemTrust";
        cert["Thumbprint"] = newThumb;
        cert["StoreName"] = "My";
        cert["StoreLocation"] = "LocalMachine";
        cert["PfxPath"] = "";
        cert["PfxPasswordProtectedPath"] = "";

        // Strip legacy Kestrel endpoints if present.
        app.Remove("Kestrel");

        TlsJsonConfig.AtomicWrite(snapshot.AppsettingsProductionPath, app);

        JsonObject op;
        try { op = JsonNode.Parse(snapshot.OperationalJson) as JsonObject ?? new JsonObject(); }
        catch { op = new JsonObject(); }

        op["Port"] = snapshot.Port;
        op["PublicHostname"] = request.Hostname;
        op["PublicBaseUrl"] = publicUrl;
        op["CertificateMode"] = "Store";
        op["CertificateValidationMode"] = "SystemTrust";
        op["CertificateThumbprint"] = newThumb;
        // Preserve firewall rule name exactly.
        if (!string.IsNullOrEmpty(snapshot.FirewallRuleName))
            op["FirewallRuleName"] = snapshot.FirewallRuleName;
        if (!string.IsNullOrEmpty(snapshot.ServiceName))
            op["ServiceName"] = snapshot.ServiceName;
        op["InstallDir"] = request.InstallDir;

        Directory.CreateDirectory(TlsConfigPaths.ProgramDataRoot);
        TlsJsonConfig.AtomicWrite(snapshot.OperationalProgramDataPath, op);

        var installCopy = TlsConfigPaths.OperationalInstallCopy(request.InstallDir);
        Directory.CreateDirectory(Path.GetDirectoryName(installCopy)!);
        TlsJsonConfig.AtomicWrite(installCopy, op);
    }

    private static void RestoreSnapshot(TlsConfigureSnapshot snapshot)
    {
        File.WriteAllText(snapshot.AppsettingsProductionPath, snapshot.AppsettingsJson);
        Directory.CreateDirectory(Path.GetDirectoryName(snapshot.OperationalProgramDataPath)!);
        File.WriteAllText(snapshot.OperationalProgramDataPath, snapshot.OperationalJson);
        if (!string.IsNullOrEmpty(snapshot.OperationalInstallCopyPath) &&
            snapshot.OperationalInstallCopyJson is not null)
        {
            File.WriteAllText(snapshot.OperationalInstallCopyPath, snapshot.OperationalInstallCopyJson);
        }
    }

    private (bool Ok, string Message) WaitHealthy(TlsConfigureRequest request, CancellationToken ct)
    {
        if (request.HealthProbeAsync is not null)
            return request.HealthProbeAsync(ct).GetAwaiter().GetResult();

        if (request.SkipServiceControl)
            return (true, "service control skipped");

        var deadline = DateTime.UtcNow + request.HealthTimeout;
        Exception? last = null;
        while (DateTime.UtcNow < deadline)
        {
            ct.ThrowIfCancellationRequested();
            try
            {
                var status = _services.Status(request.ServiceName);
                if (!string.Equals(status, "Running", StringComparison.OrdinalIgnoreCase))
                {
                    last = new InvalidOperationException("service status=" + status);
                }
                else
                {
                    // Local HTTPS probe with SNI = PublicHostname against Hosting:Port.
                    try
                    {
                        var snap = CaptureSnapshot(request.InstallDir);
                        var dict = new Dictionary<string, string?>
                        {
                            ["Hosting:Port"] = snap.Port.ToString(),
                            ["Hosting:PublicHostname"] = snap.PublicHostname,
                            ["Certificate:Mode"] = snap.CertificateMode,
                            ["Certificate:ValidationMode"] = snap.CertificateValidationMode,
                            ["Certificate:Thumbprint"] = snap.CertificateThumbprint
                        };
                        IConfiguration config = new ConfigurationBuilder()
                            .AddInMemoryCollection(dict!)
                            .Build();
                        var probe = LocalTlsHealth.ProbeAsync(config, ct).GetAwaiter().GetResult();
                        if (probe.Ok)
                            return probe;
                        last = new InvalidOperationException(probe.Message);
                    }
                    catch (Exception ex)
                    {
                        // If probe infra fails but service is Running, keep waiting until timeout.
                        last = ex;
                    }
                }
            }
            catch (Exception ex)
            {
                last = ex;
            }

            Thread.Sleep(1000);
        }

        return (false, last?.Message ?? "health timeout");
    }

    private static string FirstNonEmpty(params string[] values) =>
        values.FirstOrDefault(v => !string.IsNullOrWhiteSpace(v)) ?? "";

    private static string GetSanDisplay(X509Certificate2 cert)
    {
        try
        {
            var san = cert.Extensions.OfType<X509SubjectAlternativeNameExtension>().FirstOrDefault();
            return san?.Format(false) ?? "";
        }
        catch
        {
            return "";
        }
    }
}
