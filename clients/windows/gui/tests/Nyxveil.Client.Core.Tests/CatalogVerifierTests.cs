using System.Net;
using System.Net.Http.Json;
using System.Text;
using System.Text.Json;
using NSec.Cryptography;
using Xunit;

namespace Nyxveil.Client.Core.Tests;

public sealed class CatalogVerifierTests
{
    private static (Key privateKey, string kid, Dictionary<string, string> keys) NewKey()
    {
        var algo = SignatureAlgorithm.Ed25519;
        var key = Key.Create(algo, new KeyCreationParameters { ExportPolicy = KeyExportPolicies.AllowPlaintextExport });
        var pub = key.PublicKey.Export(KeyBlobFormat.RawPublicKey);
        var kid = "test-key-1";
        var keys = new Dictionary<string, string> { [kid] = Convert.ToBase64String(pub) };
        return (key, kid, keys);
    }

    private static (SignedCatalogDto signed, byte[] raw) SignCatalog(Key privateKey, string kid, DateTime issued, DateTime expires)
    {
        var catalog = new CatalogDto
        {
            Version = "cat_test",
            IssuedAt = issued,
            ExpiresAt = expires,
            Locations =
            [
                new LocationDto
                {
                    LocationId = "fi-helsinki",
                    Country = "Finland",
                    CountryCode = "FI",
                    City = "Helsinki",
                    DisplayName = "Helsinki",
                    Enabled = true
                }
            ],
            Nodes = Array.Empty<NodeRegistryEntryDto>()
        };
        var payload = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
        var sig = SignatureAlgorithm.Ed25519.Sign(privateKey, payload);
        var signed = new SignedCatalogDto
        {
            Catalog = catalog,
            KeyId = kid,
            Signature = sig
        };
        var raw = JsonSerializer.SerializeToUtf8Bytes(signed);
        return (signed, raw);
    }

    [Fact]
    public void A_ValidCachedCatalog_Passes()
    {
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.Parse("2026-09-07T16:30:00Z");
        var (signed, _) = SignCatalog(key, kid,
            now.UtcDateTime.AddMinutes(-10),
            now.UtcDateTime.AddMinutes(50));
        var report = CatalogVerifier.Verify(signed, keys, now);
        Assert.Equal("PASS", report.Signature);
        Assert.Equal("PASS", report.TemporalValidation);
    }

    [Fact]
    public void B_ExpiredCache_ThenFreshValid_PassesAfterRefreshSemantics()
    {
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.Parse("2026-09-07T16:30:00Z");
        var (expired, _) = SignCatalog(key, kid,
            now.UtcDateTime.AddHours(-2),
            now.UtcDateTime.AddHours(-1));
        Assert.Throws<CatalogTemporalException>(() => CatalogVerifier.Verify(expired, keys, now));

        var (fresh, _) = SignCatalog(key, kid,
            now.UtcDateTime.AddMinutes(-1),
            now.UtcDateTime.AddMinutes(59));
        var report = CatalogVerifier.Verify(fresh, keys, now);
        Assert.Equal("PASS", report.TemporalValidation);
    }

    [Fact]
    public void C_NotYetValidCache_ThenFreshValid_Passes()
    {
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.Parse("2026-09-07T16:30:00Z");
        // Outside 5-minute skew → fail
        var (notYet, _) = SignCatalog(key, kid,
            now.UtcDateTime.AddMinutes(10),
            now.UtcDateTime.AddHours(1));
        Assert.Throws<CatalogTemporalException>(() => CatalogVerifier.Verify(notYet, keys, now));

        var (fresh, _) = SignCatalog(key, kid,
            now.UtcDateTime,
            now.UtcDateTime.AddHours(1));
        Assert.Equal("PASS", CatalogVerifier.Verify(fresh, keys, now).TemporalValidation);
    }

    [Fact]
    public void D_ExpiredCache_NetworkFailure_DoesNotUseExpired()
    {
        var dir = Path.Combine(Path.GetTempPath(), "nyxveil-cache-test-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        var prev = Environment.GetEnvironmentVariable("LOCALAPPDATA");
        try
        {
            Environment.SetEnvironmentVariable("LOCALAPPDATA", dir);
            CatalogCacheStore.Delete();

            var (key, kid, keys) = NewKey();
            var now = DateTimeOffset.UtcNow;
            var (expired, raw) = SignCatalog(key, kid,
                now.UtcDateTime.AddHours(-2),
                now.UtcDateTime.AddHours(-1));
            // Store expired as if it were cached (bypass verify on save)
            CatalogCacheStore.Save(raw, new CatalogKeysResponse { Keys = keys, Issuer = "t" });

            var handler = new SeqHandler(req =>
            {
                if (req.RequestUri!.AbsolutePath.EndsWith("/catalog-keys", StringComparison.Ordinal))
                    return Json(HttpStatusCode.OK, new CatalogKeysResponse { Keys = keys, Issuer = "t" });
                return new HttpResponseMessage(HttpStatusCode.BadGateway)
                {
                    Content = new StringContent("upstream down")
                };
            });
            using var http = new HttpClient(handler) { BaseAddress = new Uri("https://cp.test/") };
            using var cp = new ControlPlaneClient(http);
            using var boot = new SessionBootstrap(cp, new ClientSettings { ControlPlaneBaseUrl = "https://cp.test" });

            var ex = Assert.Throws<InvalidOperationException>(() =>
                boot.EnsureCatalogForConnectAsync("license-token").GetAwaiter().GetResult());
            Assert.Contains("Не удалось", ex.Message, StringComparison.Ordinal);
            Assert.DoesNotContain("истёк или ещё не начался", ex.Message, StringComparison.Ordinal);
            _ = expired; // stored expired intentionally
        }
        finally
        {
            Environment.SetEnvironmentVariable("LOCALAPPDATA", prev);
            try { Directory.Delete(dir, true); } catch { /* ignore */ }
        }
    }

    [Fact]
    public void E_UtcZ_OnWindowsUtcPlus5_Passes()
    {
        Assert.Equal(TimeSpan.FromHours(5), TimeZoneInfo.Local.BaseUtcOffset);
        var (key, kid, keys) = NewKey();
        // Unspecified wall times (as if JSON lacked Z) must be treated as UTC, not local.
        var issued = DateTime.SpecifyKind(
            DateTime.Parse("2026-09-07T16:26:33.8327094", System.Globalization.CultureInfo.InvariantCulture),
            DateTimeKind.Unspecified);
        var expires = DateTime.SpecifyKind(
            DateTime.Parse("2026-09-07T17:26:33.8267094", System.Globalization.CultureInfo.InvariantCulture),
            DateTimeKind.Unspecified);
        var (signed, _) = SignCatalog(key, kid, issued, expires);
        var now = DateTimeOffset.Parse("2026-09-07T16:30:00Z");
        var report = CatalogVerifier.Verify(signed, keys, now);
        Assert.Equal("PASS", report.TemporalValidation);
        // Old ToUniversalTime bug would shift -5h → appear expired
        Assert.True(report.RemainingSeconds > 3000);
    }

    [Fact]
    public void F_Boundary_NowEqualsIssuedAndExpires_Pass()
    {
        var (key, kid, keys) = NewKey();
        var issued = DateTimeOffset.Parse("2026-09-07T16:00:00Z");
        var expires = DateTimeOffset.Parse("2026-09-07T17:00:00Z");
        var (signed, _) = SignCatalog(key, kid, issued.UtcDateTime, expires.UtcDateTime);

        Assert.Equal("PASS", CatalogVerifier.Verify(signed, keys, issued).TemporalValidation);
        Assert.Equal("PASS", CatalogVerifier.Verify(signed, keys, expires).TemporalValidation);
        Assert.Throws<CatalogTemporalException>(() =>
            CatalogVerifier.Verify(signed, keys, expires.AddTicks(1)));
    }

    [Fact]
    public void G_StalePersistentCache_FirstConnectRefreshes()
    {
        var dir = Path.Combine(Path.GetTempPath(), "nyxveil-cache-test-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        var prev = Environment.GetEnvironmentVariable("LOCALAPPDATA");
        try
        {
            Environment.SetEnvironmentVariable("LOCALAPPDATA", dir);
            CatalogCacheStore.Delete();

            var (key, kid, keys) = NewKey();
            var now = DateTimeOffset.UtcNow;
            var (_, staleRaw) = SignCatalog(key, kid,
                now.UtcDateTime.AddHours(-3),
                now.UtcDateTime.AddHours(-2));
            CatalogCacheStore.Save(staleRaw, new CatalogKeysResponse { Keys = keys, Issuer = "t" });

            var (fresh, freshRaw) = SignCatalog(key, kid,
                now.UtcDateTime.AddMinutes(-1),
                now.UtcDateTime.AddMinutes(59));

            var handler = new SeqHandler(req =>
            {
                if (req.RequestUri!.AbsolutePath.EndsWith("/catalog-keys", StringComparison.Ordinal))
                    return Json(HttpStatusCode.OK, new CatalogKeysResponse { Keys = keys, Issuer = "t" });
                return new HttpResponseMessage(HttpStatusCode.OK)
                {
                    Content = new ByteArrayContent(freshRaw)
                    {
                        Headers = { ContentType = new System.Net.Http.Headers.MediaTypeHeaderValue("application/json") }
                    }
                };
            });
            using var http = new HttpClient(handler) { BaseAddress = new Uri("https://cp.test/") };
            using var cp = new ControlPlaneClient(http);
            using var boot = new SessionBootstrap(cp, new ClientSettings { ControlPlaneBaseUrl = "https://cp.test" });
            var (got, raw, _) = boot.EnsureCatalogForConnectAsync("license-token").GetAwaiter().GetResult();
            Assert.Equal(fresh.Catalog.Version, got.Catalog.Version);
            Assert.Equal(freshRaw, raw);
            // Cache replaced
            var cached = CatalogCacheStore.TryLoad();
            Assert.NotNull(cached);
            Assert.Equal(Convert.ToBase64String(freshRaw), cached!.SignedCatalogJsonBase64);
        }
        finally
        {
            Environment.SetEnvironmentVariable("LOCALAPPDATA", prev);
            try { Directory.Delete(dir, true); } catch { /* ignore */ }
        }
    }

    [Fact]
    public void H_SignatureVerification_RejectsTamper()
    {
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.UtcNow;
        var (signed, _) = SignCatalog(key, kid, now.UtcDateTime.AddMinutes(-1), now.UtcDateTime.AddHours(1));
        signed.Catalog.Version = "tampered";
        var ex = Assert.Throws<InvalidOperationException>(() => CatalogVerifier.Verify(signed, keys, now));
        Assert.Contains("Подпись", ex.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void H2_IssuedAtSkewWithinFiveMinutes_Passes()
    {
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.Parse("2026-09-07T16:30:00Z");
        var (signed, _) = SignCatalog(key, kid,
            now.UtcDateTime.AddMinutes(4),
            now.UtcDateTime.AddHours(1));
        Assert.Equal("PASS", CatalogVerifier.Verify(signed, keys, now).TemporalValidation);
    }

    [Fact]
    public void I_Cp112Compat_FieldsIssuedExpiresOnly()
    {
        // CP 1.1.2 catalog contract: issued_at + expires_at (no separate not_before).
        var (key, kid, keys) = NewKey();
        var now = DateTimeOffset.UtcNow;
        var (signed, raw) = SignCatalog(key, kid, now.UtcDateTime.AddMinutes(-1), now.UtcDateTime.AddHours(1));
        var json = Encoding.UTF8.GetString(raw);
        Assert.Contains("\"issued_at\"", json, StringComparison.Ordinal);
        Assert.Contains("\"expires_at\"", json, StringComparison.Ordinal);
        Assert.DoesNotContain("not_before", json, StringComparison.Ordinal);
        Assert.DoesNotContain("valid_until", json, StringComparison.Ordinal);
        CatalogVerifier.Verify(CatalogVerifier.Parse(raw), keys, now);
    }

    private static HttpResponseMessage Json(HttpStatusCode code, object body) =>
        new(code) { Content = JsonContent.Create(body) };

    private sealed class SeqHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, HttpResponseMessage> _fn;
        public SeqHandler(Func<HttpRequestMessage, HttpResponseMessage> fn) => _fn = fn;
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
            Task.FromResult(_fn(request));
    }
}
