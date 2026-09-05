using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Hosting;
using Microsoft.Data.SqlClient;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class HttpsAndCertificateTests
{
    [Fact]
    public void TestMissingCertificateFailsProductionStartup()
    {
        var env = new FakeHostEnvironment { EnvironmentName = Environments.Production };
        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Https:RequireHttpsInProduction"] = "true",
                ["Certificate:Mode"] = "Store",
                ["Certificate:Thumbprint"] = "",
                ["Urls"] = "https://0.0.0.0:8443"
            })
            .Build();

        var ex = Assert.Throws<InvalidOperationException>(() =>
            HttpsEnforcement.EnforceProductionHttps(env, config, loadedCertificate: null));

        Assert.Contains("private key", ex.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestProductionWithoutCertificateFails() => TestMissingCertificateFailsProductionStartup();

    [Fact]
    public void TestConfiguredCertificateLoads()
    {
        using var cert = CertificateLoader.CreateSelfSigned("unit-test.nyxveil.local");
        Assert.True(cert.HasPrivateKey);

        var options = new CertificateOptions { Mode = CertificateMode.SelfSigned };
        Assert.True(CertificateLoader.TryLoad(options, "unit-test.nyxveil.local", out var loaded, out var error));
        Assert.Null(error);
        Assert.NotNull(loaded);
        Assert.True(loaded!.HasPrivateKey);
        loaded.Dispose();

        var env = new FakeHostEnvironment { EnvironmentName = Environments.Production };
        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Https:RequireHttpsInProduction"] = "true",
                ["Certificate:Mode"] = "SelfSigned",
                ["Hosting:PublicHostname"] = "unit-test.nyxveil.local"
            })
            .Build();

        // Production with SelfSigned and no thumbprint must fail when probing loader with isProduction.
        Assert.False(CertificateLoader.TryLoad(
            new CertificateOptions { Mode = CertificateMode.SelfSigned },
            "unit-test.nyxveil.local",
            isProduction: true,
            out _,
            out var prodError));
        Assert.Contains("Production", prodError ?? "", StringComparison.OrdinalIgnoreCase);

        HttpsEnforcement.EnforceProductionHttps(env, config, loadedCertificate: cert);
    }

    [Fact]
    public void TestConfiguredThumbprintLoadsPrivateKey()
    {
        using var cert = CertificateLoader.CreateSelfSigned("thumb-test.nyxveil.local");
        Assert.True(cert.HasPrivateKey);
        Assert.False(string.IsNullOrWhiteSpace(cert.Thumbprint));

        // Preferred production shape after import.
        var preferred = CertificateLoader.PreferredStoreConfig(cert.Thumbprint);
        Assert.Equal(CertificateMode.Store, preferred.Mode);
        Assert.Equal(CertificateLoader.NormalizeThumbprint(cert.Thumbprint), preferred.Thumbprint);
    }

    [Fact]
    public void TestSelfSignedWithThumbprintUsesStoreNotRegen()
    {
        var options = new CertificateOptions
        {
            Mode = CertificateMode.SelfSigned,
            Thumbprint = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
        };

        // Must attempt store load (missing cert) — not CreateSelfSigned (which would succeed).
        Assert.False(CertificateLoader.TryLoad(options, "localhost", out var loaded, out var error));
        Assert.Null(loaded);
        Assert.Contains("thumbprint", error ?? "", StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("unknown", error ?? "", StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestSelfSignedCertificatePersistsAcrossRestart()
    {
        // Simulate restart: Mode=SelfSigned + Thumbprint → store path (same as persisted install).
        var options = new CertificateOptions
        {
            Mode = CertificateMode.SelfSigned,
            Thumbprint = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
        };

        Assert.False(CertificateLoader.TryLoad(options, "localhost", isProduction: true, out var a, out var err1));
        Assert.False(CertificateLoader.TryLoad(options, "localhost", isProduction: true, out var b, out var err2));
        Assert.Null(a);
        Assert.Null(b);
        Assert.Contains("thumbprint", err1 ?? "", StringComparison.OrdinalIgnoreCase);
        Assert.Equal(err1, err2);
    }

    [Fact]
    public void TestPfxImportUsesCertificateStore()
    {
        var afterImport = CertificateLoader.PreferredStoreConfig("CCDDEEFF00112233445566778899AABBCCDDEEFF");
        Assert.Equal(CertificateMode.Store, afterImport.Mode);
        Assert.Equal("My", afterImport.StoreName);
        Assert.Equal("LocalMachine", afterImport.StoreLocation);
        Assert.NotEqual(CertificateMode.Pfx, afterImport.Mode);
        Assert.NotEqual(CertificateMode.SelfSigned, afterImport.Mode);
    }

    [Fact]
    public void TestOnlyOneKestrelHttpsEndpointConfigured()
    {
        Assert.Equal(8443, HostingOptions.DefaultPort);
        Assert.Equal(8443, new HostingOptions().Port);

        var examplePath = FindAppsettingsExample();
        Assert.True(File.Exists(examplePath), "appsettings.Example.json not found");
        using var doc = JsonDocument.Parse(File.ReadAllText(examplePath));
        Assert.False(doc.RootElement.TryGetProperty("Kestrel", out _),
            "appsettings.Example.json must not define Kestrel:Endpoints (Hosting is SoT)");
        Assert.True(doc.RootElement.TryGetProperty("Hosting", out var hosting));
        Assert.Equal(8443, hosting.GetProperty("Port").GetInt32());

        var webSettings = FindWebAppsettings();
        using var webDoc = JsonDocument.Parse(File.ReadAllText(webSettings));
        Assert.False(webDoc.RootElement.TryGetProperty("Kestrel", out _),
            "appsettings.json must not define Kestrel:Endpoints");
        Assert.Equal(8443, webDoc.RootElement.GetProperty("Hosting").GetProperty("Port").GetInt32());
    }

    [Fact]
    public void HostingOptions_RejectsInvalidPorts()
    {
        var validator = new HostingOptionsValidator();
        Assert.True(validator.Validate(null, new HostingOptions { Port = 8443 }).Succeeded);
        Assert.True(validator.Validate(null, new HostingOptions { Port = 0 }).Succeeded); // test hosts
        Assert.False(validator.Validate(null, new HostingOptions { Port = 70000 }).Succeeded);
        Assert.False(validator.Validate(null, new HostingOptions { Port = -1 }).Succeeded);
    }

    [Fact]
    public void TestSqlAuthReadinessUsesProtectedPassword()
    {
        const string overlayPassword = "S3cret-Overlay-Password!";
        var baseCs = "Server=localhost;Database=NyxveilControlPlane;User ID=nvp;TrustServerCertificate=True;Encrypt=True";
        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:ControlPlane"] = baseCs,
                ["ConnectionStrings:SqlPassword"] = overlayPassword
            })
            .Build();

        var provider = new DatabaseConnectionStringProvider(config);
        var resolved = provider.GetConnectionString();
        var builder = new SqlConnectionStringBuilder(resolved);
        Assert.Equal(overlayPassword, builder.Password);

        var redacted = DatabaseConnectionStringProvider.DescribeWithoutPassword(resolved);
        Assert.DoesNotContain(overlayPassword, redacted, StringComparison.Ordinal);
        Assert.Contains("********", redacted, StringComparison.Ordinal);

        // ApplySqlPasswordOverlay helper used by provider
        var applied = DpapiSecretsConfigurationProvider.ApplySqlPasswordOverlay(baseCs, overlayPassword);
        Assert.Equal(overlayPassword, new SqlConnectionStringBuilder(applied).Password);
    }

    [Fact]
    public void SigningKeyBackup_RoundTripsAesGcmPayload()
    {
        var plain = """{"Keys":[{"KeyId":"k1"}]}"""u8.ToArray();
        var encrypted = SigningKeyBackupService.Encrypt(plain, "TestBackup!Password");
        var decrypted = SigningKeyBackupService.Decrypt(encrypted, "TestBackup!Password");
        Assert.Equal(plain, decrypted);
    }

    [Fact]
    public void TestPortableSigningKeyBackupRoundTrip()
    {
        var seed = RandomNumberGenerator.GetBytes(32);
        var encrypted = PortableSecretCrypto.Encrypt(seed, "Portable!Pass1");
        var bundle = new PortableKeyBundle
        {
            FormatVersion = 1,
            CreatedAt = DateTimeOffset.UtcNow,
            KeyId = "cp-key-test",
            Algorithm = "Ed25519",
            Status = "Current",
            PublicKeyB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
            SaltB64 = Convert.ToBase64String(encrypted.Salt),
            Iterations = encrypted.Iterations,
            NonceB64 = Convert.ToBase64String(encrypted.Nonce),
            CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
            TagB64 = Convert.ToBase64String(encrypted.Tag)
        };

        var json = PortableSecretCrypto.ToJson(bundle);
        var round = PortableSecretCrypto.FromJson<PortableKeyBundle>(json);
        var blob = new PortableSecretCrypto.EncryptedBlob(
            Convert.FromBase64String(round.SaltB64),
            round.Iterations,
            Convert.FromBase64String(round.NonceB64),
            Convert.FromBase64String(round.CiphertextB64),
            Convert.FromBase64String(round.TagB64));
        var decrypted = PortableSecretCrypto.Decrypt(blob, "Portable!Pass1");
        Assert.Equal(seed, decrypted);
        Assert.Contains("ciphertext_b64", json, StringComparison.Ordinal);
        Assert.DoesNotContain(Convert.ToHexString(seed), json, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestLicenseKekPortableRecoveryRoundTrip()
    {
        var kekHex = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
        var plain = Encoding.UTF8.GetBytes(kekHex);
        var encrypted = PortableSecretCrypto.Encrypt(plain, "KekBackup!Pass1");
        var bundle = new PortableKekBundle
        {
            FormatVersion = 1,
            CreatedAt = DateTimeOffset.UtcNow,
            Algorithm = "LicenseKekHex",
            SaltB64 = Convert.ToBase64String(encrypted.Salt),
            Iterations = encrypted.Iterations,
            NonceB64 = Convert.ToBase64String(encrypted.Nonce),
            CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
            TagB64 = Convert.ToBase64String(encrypted.Tag)
        };

        var json = PortableSecretCrypto.ToJson(bundle);
        var round = PortableSecretCrypto.FromJson<PortableKekBundle>(json);
        var blob = new PortableSecretCrypto.EncryptedBlob(
            Convert.FromBase64String(round.SaltB64),
            round.Iterations,
            Convert.FromBase64String(round.NonceB64),
            Convert.FromBase64String(round.CiphertextB64),
            Convert.FromBase64String(round.TagB64));
        var decrypted = Encoding.UTF8.GetString(PortableSecretCrypto.Decrypt(blob, "KekBackup!Pass1"));
        Assert.Equal(kekHex, decrypted);
        Assert.DoesNotContain(kekHex, json, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void TestRecoveryBundleContainsNoPlaintextSecrets()
    {
        var bundle = new ControlPlaneRecoveryBundle
        {
            FormatVersion = 1,
            CreatedAt = DateTimeOffset.UtcNow,
            SigningKeys =
            [
                new PortableKeyBundle
                {
                    KeyId = "k1",
                    Algorithm = "Ed25519",
                    PublicKeyB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
                    SaltB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(16)),
                    Iterations = 210_000,
                    NonceB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(12)),
                    CiphertextB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
                    TagB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(16))
                }
            ],
            LicenseKek = new PortableKekBundle
            {
                SaltB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(16)),
                Iterations = 210_000,
                NonceB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(12)),
                CiphertextB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
                TagB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(16))
            }
        };

        var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(bundle));
        ControlPlaneRecoveryService.AssertNoPlaintextSecrets(json);

        var bad = Encoding.UTF8.GetBytes("""{"SqlPassword":"nope","signing_keys":[]}""");
        Assert.Throws<InvalidOperationException>(() =>
            ControlPlaneRecoveryService.AssertNoPlaintextSecrets(bad));

        var badKek = Encoding.UTF8.GetBytes("""{"license_kek_hex":"00","signing_keys":[]}""");
        Assert.Throws<InvalidOperationException>(() =>
            ControlPlaneRecoveryService.AssertNoPlaintextSecrets(badKek));
    }

    [Fact]
    public async Task TestUnifiedRecoveryRestoresSigningAndKek()
    {
        var seed = RandomNumberGenerator.GetBytes(32);
        var kekHex = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
        const string password = "UnifiedRecovery!Pass1";

        var signing = new RecordingSigningKeyBackup(seed, password);
        var kek = new RecordingKekBackup(kekHex, password);
        var recovery = new ControlPlaneRecoveryService(signing, kek);

        await using var export = new MemoryStream();
        await recovery.ExportRecoveryBundleAsync(export, password);
        var bytes = export.ToArray();
        ControlPlaneRecoveryService.AssertNoPlaintextSecrets(bytes);
        Assert.DoesNotContain(kekHex, Encoding.UTF8.GetString(bytes), StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain(Convert.ToHexString(seed), Encoding.UTF8.GetString(bytes), StringComparison.OrdinalIgnoreCase);

        await using var import = new MemoryStream(bytes);
        await recovery.ImportRecoveryBundleAsync(import, password, force: true);

        Assert.True(signing.Imported);
        Assert.True(kek.Imported);
        Assert.Equal(seed, signing.ImportedSeed);
        Assert.Equal(kekHex, kek.ImportedKekHex);
    }

    private sealed class RecordingSigningKeyBackup : ISigningKeyBackupService
    {
        private readonly byte[] _seed;
        private readonly string _password;

        public RecordingSigningKeyBackup(byte[] seed, string password)
        {
            _seed = seed;
            _password = password;
        }

        public bool Imported { get; private set; }
        public byte[]? ImportedSeed { get; private set; }

        public Task ExportPortableAsync(Stream output, string password, CancellationToken cancellationToken = default)
        {
            var encrypted = PortableSecretCrypto.Encrypt(_seed, password);
            var doc = new
            {
                format_version = 1,
                created_at = DateTimeOffset.UtcNow,
                keys = new[]
                {
                    new PortableKeyBundle
                    {
                        KeyId = "cp-key-unified",
                        Algorithm = "Ed25519",
                        Status = "Current",
                        PublicKeyB64 = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)),
                        SaltB64 = Convert.ToBase64String(encrypted.Salt),
                        Iterations = encrypted.Iterations,
                        NonceB64 = Convert.ToBase64String(encrypted.Nonce),
                        CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
                        TagB64 = Convert.ToBase64String(encrypted.Tag)
                    }
                }
            };
            var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(doc));
            return output.WriteAsync(json, cancellationToken).AsTask();
        }

        public async Task ImportPortableAsync(
            Stream input,
            string password,
            bool force = false,
            CancellationToken cancellationToken = default)
        {
            using var ms = new MemoryStream();
            await input.CopyToAsync(ms, cancellationToken);
            using var doc = JsonDocument.Parse(ms.ToArray());
            var key = doc.RootElement.GetProperty("keys")[0];
            var blob = new PortableSecretCrypto.EncryptedBlob(
                Convert.FromBase64String(key.GetProperty("salt_b64").GetString()!),
                key.GetProperty("iterations").GetInt32(),
                Convert.FromBase64String(key.GetProperty("nonce_b64").GetString()!),
                Convert.FromBase64String(key.GetProperty("ciphertext_b64").GetString()!),
                Convert.FromBase64String(key.GetProperty("tag_b64").GetString()!));
            ImportedSeed = PortableSecretCrypto.Decrypt(blob, password);
            Imported = true;
            Assert.Equal(_password, password);
        }

        public Task ExportAsync(Stream output, string password, CancellationToken cancellationToken = default) =>
            ExportPortableAsync(output, password, cancellationToken);

        public Task ImportAsync(Stream input, string password, bool force = false, CancellationToken cancellationToken = default) =>
            ImportPortableAsync(input, password, force, cancellationToken);

        public Task ExportEncryptedZipAsync(Stream output, string password, CancellationToken cancellationToken = default) =>
            ExportPortableAsync(output, password, cancellationToken);

        public Task ImportEncryptedZipAsync(
            Stream input,
            string password,
            bool force = false,
            CancellationToken cancellationToken = default) =>
            ImportPortableAsync(input, password, force, cancellationToken);
    }

    private sealed class RecordingKekBackup : ILicenseKekBackupService
    {
        private readonly string _kekHex;
        private readonly string _password;

        public RecordingKekBackup(string kekHex, string password)
        {
            _kekHex = kekHex;
            _password = password;
        }

        public bool Imported { get; private set; }
        public string? ImportedKekHex { get; private set; }

        public Task ExportAsync(Stream output, string password, CancellationToken cancellationToken = default)
        {
            var encrypted = PortableSecretCrypto.Encrypt(Encoding.UTF8.GetBytes(_kekHex), password);
            var bundle = new PortableKekBundle
            {
                FormatVersion = 1,
                CreatedAt = DateTimeOffset.UtcNow,
                Algorithm = "LicenseKekHex",
                SaltB64 = Convert.ToBase64String(encrypted.Salt),
                Iterations = encrypted.Iterations,
                NonceB64 = Convert.ToBase64String(encrypted.Nonce),
                CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
                TagB64 = Convert.ToBase64String(encrypted.Tag)
            };
            var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(bundle));
            return output.WriteAsync(json, cancellationToken).AsTask();
        }

        public async Task ImportAsync(Stream input, string password, CancellationToken cancellationToken = default)
        {
            using var ms = new MemoryStream();
            await input.CopyToAsync(ms, cancellationToken);
            var bundle = PortableSecretCrypto.FromJson<PortableKekBundle>(ms.ToArray());
            var blob = new PortableSecretCrypto.EncryptedBlob(
                Convert.FromBase64String(bundle.SaltB64),
                bundle.Iterations,
                Convert.FromBase64String(bundle.NonceB64),
                Convert.FromBase64String(bundle.CiphertextB64),
                Convert.FromBase64String(bundle.TagB64));
            ImportedKekHex = Encoding.UTF8.GetString(PortableSecretCrypto.Decrypt(blob, password));
            Imported = true;
            Assert.Equal(_password, password);
        }
    }

    [Fact]
    public void TestSetupDisabledInProduction_DefaultOptions()
    {
        var setup = new SetupOptions();
        Assert.False(setup.AllowWebBootstrap);

        var examplePath = FindAppsettingsExample();
        using var doc = JsonDocument.Parse(File.ReadAllText(examplePath));
        Assert.False(doc.RootElement.GetProperty("Setup").GetProperty("AllowWebBootstrap").GetBoolean());

        var webSettings = FindWebAppsettings();
        using var webDoc = JsonDocument.Parse(File.ReadAllText(webSettings));
        Assert.False(webDoc.RootElement.GetProperty("Setup").GetProperty("AllowWebBootstrap").GetBoolean());
    }

    private static string FindAppsettingsExample()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            var candidate = Path.Combine(dir.FullName, "appsettings.Example.json");
            if (File.Exists(candidate))
                return candidate;
            var licensing = Path.Combine(dir.FullName, "licensing", "appsettings.Example.json");
            if (File.Exists(licensing))
                return licensing;
            dir = dir.Parent;
        }

        return Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "..", "..", "..", "appsettings.Example.json"));
    }

    private static string FindWebAppsettings()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            var candidate = Path.Combine(dir.FullName, "src", "Nyxveil.ControlPlane.Web", "appsettings.json");
            if (File.Exists(candidate))
                return candidate;
            var licensing = Path.Combine(dir.FullName, "licensing", "src", "Nyxveil.ControlPlane.Web", "appsettings.json");
            if (File.Exists(licensing))
                return licensing;
            dir = dir.Parent;
        }

        return Path.GetFullPath(Path.Combine(
            AppContext.BaseDirectory, "..", "..", "..", "..", "src", "Nyxveil.ControlPlane.Web", "appsettings.json"));
    }

    private sealed class FakeHostEnvironment : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = Environments.Production;
        public string ApplicationName { get; set; } = "Nyxveil.ControlPlane.Tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}
