using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Release-blocking Frozen Core catalog canonicalization regressions.
/// Golden SHAs are produced by core/controlplane/catalog canonicalPayload (Go encoding/json).
/// </summary>
public sealed class CatalogCanonicalCrossLanguageTests
{
    [Fact]
    public void ProductionLikeFixture_MatchesFrozenCoreCanonicalSha()
    {
        var issued = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Utc).AddTicks(1234567);
        var catalog = new CatalogDto
        {
            Version = "cat_test_fixture_1",
            IssuedAt = issued,
            ExpiresAt = issued.AddHours(1),
            Locations =
            [
                new LocationDto
                {
                    LocationId = "fi-helsinki", Country = "Finland", CountryCode = "FI",
                    City = "Helsinki", DisplayName = "Helsinki", Enabled = true
                }
            ],
            Nodes =
            [
                new NodeRegistryEntryDto
                {
                    NodeId = "nv-test-227e939e",
                    LocationId = "fi-helsinki",
                    Country = "Finland",
                    City = "Helsinki",
                    DisplayName = "fi-hel-01",
                    Status = "healthy",
                    Enabled = true,
                    ProtocolVersion = 1,
                    ServerVersion = "1.1.4",
                    ServerName = "fi-hel-01.nyxveil.ru",
                    SpkiPin = Enumerable.Range(1, 32).Select(i => (byte)i).ToArray(),
                    Capacity = 100,
                    CurrentSessions = 3,
                    Endpoints =
                    [
                        new EndpointDto { Host = "fi-hel-01.nyxveil.ru", Port = 443, Profiles = ["tls-tcp-443"] }
                    ],
                    Health = new HealthInfoDto
                    {
                        Healthy = true,
                        LatencyMs = 12.5,
                        SessionCount = 3,
                        CpuPercent = 0.268366320026836,
                        MemoryPercent = 54.67981109019452
                    },
                    LastSeen = issued
                }
            ]
        };

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
        Assert.Equal(
            "6f4175e581e7fd5537f6c0a7dda70bea030c64b0dfbdd24c7d85aabf06dcdf7f",
            Convert.ToHexString(SHA256.HashData(payload)).ToLowerInvariant());
    }

    [Theory]
    [InlineData(0.0, "0")]
    [InlineData(1.0, "1")]
    [InlineData(0.1, "0.1")]
    [InlineData(1.5, "1.5")]
    [InlineData(1e-10, "1e-10")]
    [InlineData(1e21, "1e+21")]
    [InlineData(0.268366320026836, "0.268366320026836")]
    [InlineData(54.67981109019452, "54.67981109019452")]
    public void GoFloatFormatting_MatchesFrozenCore(double value, string expected)
    {
        Assert.Equal(expected, GoJsonNumber.FormatFloat64(value));
    }

    [Fact]
    public void NegativeZero_FormatsAsMinusZero()
    {
        Assert.Equal("-0", GoJsonNumber.FormatFloat64(double.NegativeZero));
    }

    [Fact]
    public void UnspecifiedDateTime_IsTreatedAsUtc_NotHostLocal()
    {
        var unspecified = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Unspecified);
        var catalog = BaseCatalog(unspecified);
        catalog.Nodes[0].LastSeen = unspecified;
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        Assert.Contains("\"issued_at\":\"2026-09-07T10:00:00Z\"", json);
        Assert.Contains("\"last_seen\":\"2026-09-07T10:00:00Z\"", json);
        Assert.DoesNotContain("05:00:00Z", json);
    }

    [Fact]
    public void EmptyServerNameAndSpki_AreOmittedLikeGoOmitEmpty()
    {
        var catalog = BaseCatalog(DateTime.UnixEpoch);
        catalog.Nodes[0].ServerName = "";
        catalog.Nodes[0].SpkiPin = Array.Empty<byte>();
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        Assert.DoesNotContain("server_name", json);
        Assert.DoesNotContain("spki_pin", json);
    }

    [Fact]
    public void HtmlAndUnicode_MatchGoEncodingJsonEscaping()
    {
        var catalog = BaseCatalog(DateTime.UnixEpoch);
        catalog.Nodes[0].DisplayName = "A&B<C>";
        catalog.Locations[0].City = "Helsinki—metro";
        catalog.Locations[0].DisplayName = "Привет";
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        Assert.Contains("A\\u0026B\\u003cC\\u003e", json);
        Assert.Contains("Helsinki—metro", json);
        Assert.Contains("Привет", json);
        Assert.DoesNotContain("\\u2014", json);
        Assert.DoesNotContain("\\u003C", json);
    }

    [Theory]
    [InlineData(0)]
    [InlineData(100_000_000)] // .1s
    [InlineData(123_000_000)] // .123s
    [InlineData(123_456_000)] // .123456s
    [InlineData(120_000_000)] // .12s trailing zero trim
    [InlineData(123_456_700)] // .1234567s
    public void TimestampFractionalDigits_TrimLikeGoRfc3339Nano(long nanos)
    {
        var issued = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Utc).AddTicks(nanos / 100);
        var catalog = BaseCatalog(issued);
        catalog.Nodes[0].LastSeen = issued;
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        // Go never emits trailing fractional zeros.
        Assert.DoesNotContain(".1200000Z", json);
        Assert.DoesNotContain(".1230000Z", json);
        if (nanos == 0)
            Assert.Contains("\"issued_at\":\"2026-09-07T10:00:00Z\"", json);
        if (nanos == 100_000_000)
            Assert.Contains("\"issued_at\":\"2026-09-07T10:00:00.1Z\"", json);
        if (nanos == 120_000_000)
            Assert.Contains("\"issued_at\":\"2026-09-07T10:00:00.12Z\"", json);
    }

    [Fact]
    public void EmptyLocationsAndNodes_SerializeAsNull()
    {
        var catalog = new CatalogDto
        {
            Version = "v",
            IssuedAt = DateTime.UnixEpoch,
            ExpiresAt = DateTime.UnixEpoch.AddHours(1),
            Locations = Array.Empty<LocationDto>(),
            Nodes = Array.Empty<NodeRegistryEntryDto>()
        };
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        Assert.Contains("\"locations\":null", json);
        Assert.Contains("\"nodes\":null", json);
    }

    [Fact]
    public void UnorderedNodesAndLocations_AreSortedById()
    {
        var catalog = new CatalogDto
        {
            Version = "v",
            IssuedAt = DateTime.UnixEpoch,
            ExpiresAt = DateTime.UnixEpoch.AddHours(1),
            Locations =
            [
                new LocationDto { LocationId = "z" },
                new LocationDto { LocationId = "a" }
            ],
            Nodes =
            [
                new NodeRegistryEntryDto
                {
                    NodeId = "n-b",
                    Endpoints = [new EndpointDto { Host = "h", Port = 1, Profiles = ["tls-tcp-443"] }],
                    LastSeen = DateTime.UnixEpoch
                },
                new NodeRegistryEntryDto
                {
                    NodeId = "n-a",
                    Endpoints = [new EndpointDto { Host = "h", Port = 1, Profiles = ["tls-tcp-443"] }],
                    LastSeen = DateTime.UnixEpoch
                }
            ]
        };
        var json = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(catalog));
        Assert.True(json.IndexOf("\"location_id\":\"a\"", StringComparison.Ordinal) <
                    json.IndexOf("\"location_id\":\"z\"", StringComparison.Ordinal));
        Assert.True(json.IndexOf("\"node_id\":\"n-a\"", StringComparison.Ordinal) <
                    json.IndexOf("\"node_id\":\"n-b\"", StringComparison.Ordinal));
    }

    [Fact]
    public void CsSign_RoundTripsThroughJson_AndSelfVerifies()
    {
        var issued = DateTime.UtcNow.AddMinutes(-1);
        var catalog = BaseCatalog(issued);
        catalog.Nodes[0].ServerName = "fi-hel-01.nyxveil.ru";
        catalog.Nodes[0].SpkiPin = RandomNumberGenerator.GetBytes(32);
        catalog.Nodes[0].Health.CpuPercent = 0.268366320026836;
        catalog.Nodes[0].Health.MemoryPercent = 54.67981109019452;
        catalog.IssuedAt = issued;
        catalog.ExpiresAt = issued.AddHours(1);

        using var key = Key.Create(
            SignatureAlgorithm.Ed25519,
            new KeyCreationParameters { ExportPolicy = KeyExportPolicies.AllowPlaintextExport });
        var privateKey = key.Export(KeyBlobFormat.RawPrivateKey);
        var publicKey = key.PublicKey.Export(KeyBlobFormat.RawPublicKey);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
        var signature = Ed25519SigningKeyStore.Sign(privateKey, payload);
        Assert.True(Ed25519SigningKeyStore.Verify(publicKey, payload, signature));

        var signed = new SignedCatalogDto
        {
            Catalog = catalog,
            KeyId = "test-key",
            Signature = signature
        };
        var opts = new JsonSerializerOptions
        {
            DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull
        };
        opts.Converters.Add(new Rfc3339NanoDateTimeConverter());
        var httpJson = JsonSerializer.SerializeToUtf8Bytes(signed, opts);
        var parsed = JsonSerializer.Deserialize<SignedCatalogDto>(httpJson, opts)!;
        var again = CatalogCanonicalJson.BuildCanonicalPayload(parsed.Catalog);
        Assert.Equal(payload, again);
        Assert.True(Ed25519SigningKeyStore.Verify(publicKey, again, parsed.Signature));
    }

    private static CatalogDto BaseCatalog(DateTime issued) => new()
    {
        Version = "v1",
        IssuedAt = issued,
        ExpiresAt = issued.AddHours(1),
        Locations =
        [
            new LocationDto
            {
                LocationId = "a", Country = "C", CountryCode = "CC", City = "City",
                DisplayName = "Disp", Enabled = true
            }
        ],
        Nodes =
        [
            new NodeRegistryEntryDto
            {
                NodeId = "n1",
                LocationId = "a",
                Country = "C",
                City = "City",
                DisplayName = "Disp",
                Status = "healthy",
                Enabled = true,
                ProtocolVersion = 1,
                ServerVersion = "1.0.0",
                Endpoints = [new EndpointDto { Host = "h", Port = 443, Profiles = ["tls-tcp-443"] }],
                Capacity = 1,
                Health = new HealthInfoDto { Healthy = true },
                LastSeen = issued
            }
        ]
    };
}
