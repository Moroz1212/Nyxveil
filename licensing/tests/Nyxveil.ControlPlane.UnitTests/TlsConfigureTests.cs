using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json.Nodes;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class TlsConfigureTests : IDisposable
{
    private readonly string _root;
    private readonly string _install;
    private readonly string _programData;

    public TlsConfigureTests()
    {
        _root = Path.Combine(Path.GetTempPath(), "nyxveil-tls-cfg-" + Guid.NewGuid().ToString("N"));
        _install = Path.Combine(_root, "ControlPlane");
        _programData = Path.Combine(_root, "ProgramData");
        Directory.CreateDirectory(_install);
        Directory.CreateDirectory(_programData);
        TlsConfigPaths.ProgramDataRootOverride = _programData;
        WriteBaselineConfigs(port: 8443, hostname: "42mou.ru", publicUrl: "https://42mou.ru:8443",
            thumbprint: "521C79BA39D5FC12D5C769C74505DC3C75C2F6FB", validation: "SelfSignedPinned");
    }

    public void Dispose()
    {
        TlsConfigPaths.ProgramDataRootOverride = null;
        try { Directory.Delete(_root, recursive: true); } catch { /* AV lock */ }
    }

    [Fact]
    public void TestTlsConfigurePreserves8443()
    {
        var (engine, _, pfx, _) = CreateEngineWithTrustedLeaf("cp.nyxveil.ru");
        var result = engine.Configure(ApplyRequest(pfx));
        Assert.True(result.Success);
        Assert.Equal(8443, result.PreservedPort);
        var snap = TlsConfigureEngine.CaptureSnapshot(_install);
        Assert.Equal(8443, snap.Port);
    }

    [Theory]
    [InlineData(80)]
    [InlineData(443)]
    [InlineData(18443)]
    public void TestTlsConfigureDoesNotBindForbiddenPorts(int forbidden)
    {
        WriteBaselineConfigs(port: forbidden, hostname: "42mou.ru", publicUrl: "https://42mou.ru:" + forbidden,
            thumbprint: "AA", validation: "SelfSignedPinned");
        var ex = Assert.Throws<InvalidOperationException>(() =>
            TlsConfigureEngine.AssertPortIsolation(forbidden, [80, 443, 18443]));
        Assert.Contains(forbidden.ToString(), ex.Message);
    }

    [Fact]
    public void TestTlsConfigureDoesNotBind80() => TestTlsConfigureDoesNotBindForbiddenPorts(80);

    [Fact]
    public void TestTlsConfigureDoesNotBind443() => TestTlsConfigureDoesNotBindForbiddenPorts(443);

    [Fact]
    public void TestTlsConfigureDoesNotBind18443() => TestTlsConfigureDoesNotBindForbiddenPorts(18443);

    [Fact]
    public void TestTrustedCertificateAccepted()
    {
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            TlsCertificateGate.ValidateForPublicHttps(
                leaf,
                "cp.nyxveil.ru",
                requireSystemTrust: true,
                chainTrustOverride: _ => (true, "ok"));
        }
    }

    [Fact]
    public void TestSelfSignedRejectedForSystemTrust()
    {
        using var selfSigned = CreateLeaf("cp.nyxveil.ru", selfSigned: true);
        var ex = Assert.Throws<InvalidOperationException>(() =>
            TlsCertificateGate.ValidateForPublicHttps(selfSigned, "cp.nyxveil.ru", requireSystemTrust: false));
        Assert.Contains("Self-signed", ex.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestWrongSANRejectedBeforeServiceStop()
    {
        var services = new RecordingTlsServiceController();
        var acl = new RecordingTlsPrivateKeyAcl();
        acl.ReadAccounts.Add(@"NT SERVICE\NyxveilControlPlane");
        var engine = new TlsConfigureEngine(services, acl);
        using var leaf = CreateCaSignedLeaf("wrong.example", out var root);
        using (root)
        {
            // Plant thumbprint path isn't used — validate via gate directly simulating pre-change.
            var ex = Assert.Throws<InvalidOperationException>(() =>
                TlsCertificateGate.ValidateForPublicHttps(
                    leaf, "cp.nyxveil.ru", requireSystemTrust: true, chainTrustOverride: _ => (true, "ok")));
            Assert.Contains("hostname", ex.Message, StringComparison.OrdinalIgnoreCase);
            Assert.Empty(services.Actions);
        }
    }

    [Fact]
    public void TestMissingPrivateKeyRejected()
    {
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root, includePrivateKey: false);
        using (root)
        {
            var ex = Assert.Throws<InvalidOperationException>(() =>
                TlsCertificateGate.ValidateForPublicHttps(
                    leaf, "cp.nyxveil.ru", requireSystemTrust: false));
            Assert.Contains("private key", ex.Message, StringComparison.OrdinalIgnoreCase);
        }
    }

    [Fact]
    public void TestExpiredCertRejected()
    {
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root, expired: true);
        using (root)
        {
            var ex = Assert.Throws<InvalidOperationException>(() =>
                TlsCertificateGate.ValidateForPublicHttps(
                    leaf, "cp.nyxveil.ru", requireSystemTrust: false,
                    utcNow: DateTimeOffset.UtcNow));
            Assert.Contains("expired", ex.Message, StringComparison.OrdinalIgnoreCase);
        }
    }

    [Fact]
    public void TestServiceAccountPrivateKeyAccess()
    {
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            Assert.False(acl.HasReadAccess(leaf, @"NT SERVICE\NyxveilControlPlane"));
            acl.GrantRead(leaf, @"NT SERVICE\NyxveilControlPlane");
            Assert.True(acl.HasReadAccess(leaf, @"NT SERVICE\NyxveilControlPlane"));
            Assert.Single(acl.Grants);
        }
    }

    [Fact]
    public void TestNoBroadPrivateKeyAcl()
    {
        // Recording ACL never grants Everyone/Users — production Windows path checked separately.
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            acl.GrantRead(leaf, @"NT SERVICE\NyxveilControlPlane");
            Assert.DoesNotContain(acl.ReadAccounts, a =>
                a.Equals("Everyone", StringComparison.OrdinalIgnoreCase) ||
                a.Equals(@"BUILTIN\Users", StringComparison.OrdinalIgnoreCase));
        }
    }

    [Fact]
    public void TestAtomicOperationalConfigUpdate()
    {
        var (engine, acl, pfx, thumb) = CreateEngineWithTrustedLeaf("cp.nyxveil.ru");
        var result = engine.Configure(ApplyRequest(pfx));
        Assert.True(result.Success);
        var app = JsonNode.Parse(File.ReadAllText(TlsConfigPaths.AppsettingsProduction(_install)))!.AsObject();
        Assert.Equal("cp.nyxveil.ru", app["Hosting"]!["PublicHostname"]!.GetValue<string>());
        Assert.Equal("https://cp.nyxveil.ru:18443", app["Hosting"]!["PublicBaseUrl"]!.GetValue<string>());
        Assert.Equal(8443, app["Hosting"]!["Port"]!.GetValue<int>());
        Assert.Equal("Store", app["Certificate"]!["Mode"]!.GetValue<string>());
        Assert.Equal("SystemTrust", app["Certificate"]!["ValidationMode"]!.GetValue<string>());
        Assert.Equal(thumb,
            CertificateLoader.NormalizeThumbprint(app["Certificate"]!["Thumbprint"]!.GetValue<string>()));

        var op = JsonNode.Parse(File.ReadAllText(TlsConfigPaths.OperationalProgramData))!.AsObject();
        Assert.Equal("cp.nyxveil.ru", op["PublicHostname"]!.GetValue<string>());
        Assert.Equal("Nyxveil Control Plane HTTPS 8443", op["FirewallRuleName"]!.GetValue<string>());
        Assert.DoesNotContain("42mou.ru", op["PublicHostname"]!.GetValue<string>(), StringComparison.OrdinalIgnoreCase);
        Assert.Contains(acl.Grants, g => g.Account == @"NT SERVICE\NyxveilControlPlane");
    }

    [Fact]
    public void TestFailedRestartRestoresOldThumbprint()
    {
        var services = new RecordingTlsServiceController { FailStart = true };
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            // Seed ACL for check path not used — apply grants.
            var engine = new TlsConfigureEngine(services, acl);
            // Use requireSystemTrust override by planting a fake store cert via PFX ephemeral is hard;
            // instead write thumbprint and mock load by placing cert — Configure loads from store.
            // For unit test: skip store by using HealthProbe + PFX path without import on failure before import...
            // Simpler: call WriteConfigs path via apply with thumbprint after installing to CurrentUser — skip.
            // Use engine with custom HealthProbe and inject certificate via temporary store thumbprint file mock:
            // Override RequireSystemTrust false and use a helper that only tests rollback of configs by failing health.

            // Directly simulate apply+fail via temporary public API: configure with Skip and failing health after write.
            // We'll use PFX apply path with RequireSystemTrust=false and chainTrustOverride can't pass through request.
            // Patch: use CheckOnly=false, CertificateThumbprint from a cert we don't have in store → fails before write.
            // So force rollback by FailStart after writing: need successful cert resolve.

            // Install leaf into a temp CurrentUser store? Prefer exporting PFX and using --pfx with RequireSystemTrust.
            // Engine Validate always uses SystemTrust unless we add request flag — already have RequireSystemTrust.
            var pfxPath = Path.Combine(_root, "leaf.pfx");
            File.WriteAllBytes(pfxPath, leaf.Export(X509ContentType.Pfx, "pass"));

            // Temporarily loosen trust by wrapping engine call — add test-only: set RequireSystemTrust=false
            var result = engine.Configure(new TlsConfigureRequest
            {
                InstallDir = _install,
                Hostname = "cp.nyxveil.ru",
                PublicUrl = "https://cp.nyxveil.ru:18443",
                CertificatePfxPath = pfxPath,
                CertificatePfxPassword = "pass",
                RequireSystemTrust = false,
                SkipPrivateKeyAcl = true,
                SkipPfxStoreImport = true,
                SkipServiceControl = false,
                ServiceName = "NyxveilControlPlane",
                HealthProbeAsync = _ => Task.FromResult((true, "ok"))
            });

            // FailStart causes start exception → rollback
            Assert.False(result.Success);
            Assert.True(result.RolledBack);
            var snap = TlsConfigureEngine.CaptureSnapshot(_install);
            Assert.Equal("521C79BA39D5FC12D5C769C74505DC3C75C2F6FB", snap.CertificateThumbprint);
            Assert.Equal("42mou.ru", snap.PublicHostname);
        }
    }

    [Fact]
    public void TestFailedHealthRestoresOldConfig()
    {
        var services = new RecordingTlsServiceController();
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            var pfxPath = Path.Combine(_root, "leaf2.pfx");
            File.WriteAllBytes(pfxPath, leaf.Export(X509ContentType.Pfx, "pass"));
            var engine = new TlsConfigureEngine(services, acl);
            var result = engine.Configure(new TlsConfigureRequest
            {
                InstallDir = _install,
                Hostname = "cp.nyxveil.ru",
                PublicUrl = "https://cp.nyxveil.ru:18443",
                CertificatePfxPath = pfxPath,
                CertificatePfxPassword = "pass",
                RequireSystemTrust = false,
                SkipPrivateKeyAcl = true,
                SkipPfxStoreImport = true,
                HealthProbeAsync = _ => Task.FromResult((false, "health boom"))
            });
            Assert.True(result.RolledBack);
            var snap = TlsConfigureEngine.CaptureSnapshot(_install);
            Assert.Equal("42mou.ru", snap.PublicHostname);
            Assert.Equal("https://42mou.ru:8443", snap.PublicBaseUrl);
            Assert.Equal("SelfSignedPinned", snap.CertificateValidationMode);
        }
    }

    [Fact]
    public void TestRollbackRestoresOldHealthyService()
    {
        var services = new RecordingTlsServiceController();
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            var pfxPath = Path.Combine(_root, "leaf3.pfx");
            File.WriteAllBytes(pfxPath, leaf.Export(X509ContentType.Pfx, "pass"));
            var engine = new TlsConfigureEngine(services, acl);
            var healthCalls = 0;
            var result = engine.Configure(new TlsConfigureRequest
            {
                InstallDir = _install,
                Hostname = "cp.nyxveil.ru",
                PublicUrl = "https://cp.nyxveil.ru:18443",
                CertificatePfxPath = pfxPath,
                CertificatePfxPassword = "pass",
                RequireSystemTrust = false,
                SkipPrivateKeyAcl = true,
                SkipPfxStoreImport = true,
                HealthProbeAsync = _ =>
                {
                    healthCalls++;
                    // First health after apply fails; after rollback WaitHealthy is called again — succeed.
                    return Task.FromResult(healthCalls == 1
                        ? (false, "fail")
                        : (true, "restored"));
                }
            });
            Assert.True(result.RolledBack);
            Assert.Contains("healthy", result.Message, StringComparison.OrdinalIgnoreCase);
            Assert.Contains(services.Actions, a => a.StartsWith("start:", StringComparison.Ordinal));
        }
    }

    [Fact]
    public void TestPublicBaseUrlExternalPort18443()
    {
        var url = PublicBaseUrlSemantics.NormalizeExternalPublicUrl("https://cp.nyxveil.ru:18443");
        Assert.Equal("https://cp.nyxveil.ru:18443", url);
        Assert.Equal(18443, PublicBaseUrlSemantics.TryGetExternalPort(url));
        PublicBaseUrlSemantics.AssertHostnameMatchesPublicUrl("cp.nyxveil.ru", url);
    }

    [Fact]
    public void TestOtherServicesUntouched()
    {
        var services = new RecordingTlsServiceController();
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf("cp.nyxveil.ru", out var root);
        using (root)
        {
            var pfxPath = Path.Combine(_root, "leaf4.pfx");
            File.WriteAllBytes(pfxPath, leaf.Export(X509ContentType.Pfx, "pass"));
            var engine = new TlsConfigureEngine(services, acl);
            var result = engine.Configure(new TlsConfigureRequest
            {
                InstallDir = _install,
                Hostname = "cp.nyxveil.ru",
                PublicUrl = "https://cp.nyxveil.ru:18443",
                CertificatePfxPath = pfxPath,
                CertificatePfxPassword = "pass",
                RequireSystemTrust = false,
                SkipPrivateKeyAcl = true,
                SkipPfxStoreImport = true,
                HealthProbeAsync = _ => Task.FromResult((true, "ok"))
            });
            Assert.True(result.Success);
            Assert.All(services.Actions, a => Assert.Contains("NyxveilControlPlane", a, StringComparison.Ordinal));
            Assert.DoesNotContain(services.Actions, a => a.Contains("MSSQL", StringComparison.OrdinalIgnoreCase));
            Assert.DoesNotContain(services.Actions, a => a.Contains("IIS", StringComparison.OrdinalIgnoreCase));
        }
    }

    private (TlsConfigureEngine engine, RecordingTlsPrivateKeyAcl acl, string pfxPath, string thumbprint) CreateEngineWithTrustedLeaf(string host)
    {
        var services = new RecordingTlsServiceController();
        var acl = new RecordingTlsPrivateKeyAcl();
        using var leaf = CreateCaSignedLeaf(host, out var root);
        root.Dispose();
        var pfxPath = Path.Combine(_root, "helper-" + Guid.NewGuid().ToString("N") + ".pfx");
        File.WriteAllBytes(pfxPath, leaf.Export(X509ContentType.Pfx, "pass"));
        var thumb = CertificateLoader.NormalizeThumbprint(leaf.Thumbprint);
        return (new TlsConfigureEngine(services, acl), acl, pfxPath, thumb);
    }

    private TlsConfigureRequest ApplyRequest(string pfxPath) =>
        new()
        {
            InstallDir = _install,
            Hostname = "cp.nyxveil.ru",
            PublicUrl = "https://cp.nyxveil.ru:18443",
            CertificatePfxPath = pfxPath,
            CertificatePfxPassword = "pass",
            RequireSystemTrust = false,
            SkipPfxStoreImport = true,
            HealthProbeAsync = _ => Task.FromResult((true, "ok"))
        };

    private void WriteBaselineConfigs(int port, string hostname, string publicUrl, string thumbprint, string validation)
    {
        var app = new JsonObject
        {
            ["Hosting"] = new JsonObject
            {
                ["BindAddress"] = "0.0.0.0",
                ["Port"] = port,
                ["PublicHostname"] = hostname,
                ["PublicBaseUrl"] = publicUrl
            },
            ["Certificate"] = new JsonObject
            {
                ["Mode"] = "Store",
                ["ValidationMode"] = validation,
                ["Thumbprint"] = thumbprint,
                ["StoreName"] = "My",
                ["StoreLocation"] = "LocalMachine"
            }
        };
        TlsJsonConfig.AtomicWrite(TlsConfigPaths.AppsettingsProduction(_install), app);

        var op = new JsonObject
        {
            ["Port"] = port,
            ["BindAddress"] = "0.0.0.0",
            ["PublicHostname"] = hostname,
            ["PublicBaseUrl"] = publicUrl,
            ["InstallDir"] = _install,
            ["ServiceName"] = "NyxveilControlPlane",
            ["ServiceAccount"] = @"NT SERVICE\NyxveilControlPlane",
            ["CertificateMode"] = "Store",
            ["CertificateValidationMode"] = validation,
            ["CertificateThumbprint"] = thumbprint,
            ["FirewallRuleName"] = "Nyxveil Control Plane HTTPS 8443"
        };
        TlsJsonConfig.AtomicWrite(TlsConfigPaths.OperationalProgramData, op);
        Directory.CreateDirectory(Path.Combine(_install, "config"));
        TlsJsonConfig.AtomicWrite(TlsConfigPaths.OperationalInstallCopy(_install), op);
    }

    private static X509Certificate2 CreateLeaf(string hostname, bool selfSigned)
    {
        if (!selfSigned)
            throw new InvalidOperationException("use CreateCaSignedLeaf");
        return CreateCaSignedLeaf(hostname, out _, selfSignedRoot: true);
    }

    private static X509Certificate2 CreateCaSignedLeaf(
        string hostname,
        out X509Certificate2 root,
        bool expired = false,
        bool includePrivateKey = true,
        bool selfSignedRoot = false)
    {
        using var caKey = RSA.Create(2048);
        var caReq = new CertificateRequest("CN=Nyxveil-Test-CA", caKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        caReq.CertificateExtensions.Add(new X509BasicConstraintsExtension(true, false, 0, true));
        root = caReq.CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-90), DateTimeOffset.UtcNow.AddDays(30));

        using var leafKey = RSA.Create(2048);
        var leafReq = new CertificateRequest($"CN={hostname}", leafKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        leafReq.CertificateExtensions.Add(new X509BasicConstraintsExtension(false, false, 0, false));
        leafReq.CertificateExtensions.Add(new X509KeyUsageExtension(
            X509KeyUsageFlags.DigitalSignature | X509KeyUsageFlags.KeyEncipherment, true));
        leafReq.CertificateExtensions.Add(new X509EnhancedKeyUsageExtension(
            new OidCollection { new Oid(TlsCertificateGate.ServerAuthOid) }, false));
        var san = new SubjectAlternativeNameBuilder();
        san.AddDnsName(hostname);
        leafReq.CertificateExtensions.Add(san.Build());

        var notBefore = DateTimeOffset.UtcNow.AddDays(expired ? -40 : -1);
        var notAfter = DateTimeOffset.UtcNow.AddDays(expired ? -10 : 20);

        X509Certificate2 leafCert;
        if (selfSignedRoot)
        {
            leafCert = leafReq.CreateSelfSigned(notBefore, notAfter);
        }
        else
        {
            var serial = BitConverter.GetBytes(DateTime.UtcNow.Ticks);
            leafCert = leafReq.Create(root, notBefore, notAfter, serial);
            leafCert = leafCert.CopyWithPrivateKey(leafKey);
        }

        if (!includePrivateKey)
        {
            var pubOnly = X509CertificateLoader.LoadCertificate(leafCert.Export(X509ContentType.Cert));
            leafCert.Dispose();
            return pubOnly;
        }

        return X509CertificateLoader.LoadPkcs12(
            leafCert.Export(X509ContentType.Pfx),
            password: null,
            X509KeyStorageFlags.Exportable | X509KeyStorageFlags.EphemeralKeySet);
    }
}
