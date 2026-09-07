using System.Security.Cryptography;
using System.Text;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Infrastructure.Security;

if (args.Length > 0 && args[0] == "floats")
{
    foreach (var (cpu, mem) in new (double, double)[]
             {
                 (0.268366320026836, 54.67981109019452),
                 (0, 0),
                 (0.1, 1.5),
                 (1, 1),
                 (1e-10, 1e21),
                 (-0.0, -1.5),
             })
    {
        var cat = new CatalogDto
        {
            Version = "x",
            IssuedAt = DateTime.UnixEpoch,
            ExpiresAt = DateTime.UnixEpoch.AddHours(1),
            Locations = [new LocationDto { LocationId = "a" }],
            Nodes =
            [
                new NodeRegistryEntryDto
                {
                    NodeId = "n",
                    Health = new HealthInfoDto { CpuPercent = cpu, MemoryPercent = mem },
                    Endpoints = [new EndpointDto { Host = "h", Port = 1, Profiles = ["tls-tcp-443"] }],
                    LastSeen = DateTime.UnixEpoch
                }
            ]
        };
        var payload = CatalogCanonicalJson.BuildCanonicalPayload(cat);
        var json = Encoding.UTF8.GetString(payload);
        var i = json.IndexOf("\"health\":", StringComparison.Ordinal);
        var j = json.IndexOf("},\"last_seen\"", i, StringComparison.Ordinal);
        Console.WriteLine(json.Substring(i + 9, j - (i + 9)));
    }
    return;
}

if (args.Length > 0 && args[0] == "unspec-tz")
{
    var unspecified = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Unspecified);
    var utc = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Utc);
    foreach (var (label, dt) in new[] { ("unspec", unspecified), ("utc", utc) })
    {
        var cat = new CatalogDto
        {
            Version = "v",
            IssuedAt = dt,
            ExpiresAt = dt.AddHours(1),
            Locations = [new LocationDto { LocationId = "a" }],
            Nodes =
            [
                new NodeRegistryEntryDto
                {
                    NodeId = "n",
                    LastSeen = dt,
                    Endpoints = [new EndpointDto { Host = "h", Port = 1, Profiles = ["tls-tcp-443"] }],
                    Health = new HealthInfoDto()
                }
            ]
        };
        var s = Encoding.UTF8.GetString(CatalogCanonicalJson.BuildCanonicalPayload(cat));
        Console.WriteLine(label + " " + s.Substring(s.IndexOf("issued_at", StringComparison.Ordinal), 40));
    }
    return;
}

var name = args.Length > 0 ? args[0] : "canon-fixture";
var catalog = name == "canon-fixture" ? BuildCanonFixture() : BuildEdge(name);
var bytes = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
var sha = Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant();
Console.WriteLine($"len={bytes.Length} sha256={sha}");
Console.WriteLine(Encoding.UTF8.GetString(bytes));

static CatalogDto BuildCanonFixture()
{
    var issued = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Utc).AddTicks(1234567);
    var expires = issued.AddHours(1);
    var spki = Enumerable.Range(1, 32).Select(i => (byte)i).ToArray();
    return new CatalogDto
    {
        Version = "cat_test_fixture_1",
        IssuedAt = issued,
        ExpiresAt = expires,
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
                NodeId = "nv-test-227e939e", LocationId = "fi-helsinki",
                Country = "Finland", City = "Helsinki", DisplayName = "fi-hel-01",
                Status = "healthy", Enabled = true, ProtocolVersion = 1, ServerVersion = "1.1.4",
                Endpoints =
                [
                    new EndpointDto { Host = "fi-hel-01.nyxveil.ru", Port = 443, Profiles = ["tls-tcp-443"] }
                ],
                ServerName = "fi-hel-01.nyxveil.ru",
                SpkiPin = spki,
                Capacity = 100,
                CurrentSessions = 3,
                Health = new HealthInfoDto
                {
                    Healthy = true, LatencyMs = 12.5, SessionCount = 3,
                    CpuPercent = 0.268366320026836, MemoryPercent = 54.67981109019452
                },
                LastSeen = issued
            }
        ]
    };
}

static CatalogDto BuildEdge(string name)
{
    var issued = new DateTime(2026, 9, 7, 10, 0, 0, DateTimeKind.Utc);
    var expires = issued.AddHours(1);
    var catalog = new CatalogDto
    {
        Version = "v1",
        IssuedAt = issued,
        ExpiresAt = expires,
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
                NodeId = "n1", LocationId = "a", Country = "C", City = "City", DisplayName = "Disp",
                Status = "healthy", Enabled = true, ProtocolVersion = 1, ServerVersion = "1.0.0",
                Endpoints = [new EndpointDto { Host = "h", Port = 443, Profiles = ["tls-tcp-443"] }],
                Capacity = 1,
                Health = new HealthInfoDto { Healthy = true },
                LastSeen = issued
            }
        ]
    };

    switch (name)
    {
        case "negzero":
            catalog.Nodes[0].Health.CpuPercent = -0.0;
            catalog.Nodes[0].Health.MemoryPercent = 1;
            break;
        case "sci":
            catalog.Nodes[0].Health.CpuPercent = 1e-10;
            catalog.Nodes[0].Health.MemoryPercent = 1e21;
            break;
        case "html":
            catalog.Nodes[0].DisplayName = "A&B<C>";
            catalog.Locations[0].DisplayName = "X&Y";
            break;
        case "empty_server":
            catalog.Nodes[0].ServerName = "";
            break;
        case "empty_spki":
            catalog.Nodes[0].SpkiPin = Array.Empty<byte>();
            break;
        case "nil_spki":
            catalog.Nodes[0].SpkiPin = null;
            break;
        case "server_set":
            catalog.Nodes[0].ServerName = "fi-hel-01.nyxveil.ru";
            break;
        case "frac1":
            catalog.IssuedAt = issued.AddTicks(1_000_000);
            catalog.ExpiresAt = catalog.IssuedAt.AddHours(1);
            catalog.Nodes[0].LastSeen = catalog.IssuedAt;
            break;
        case "frac3":
            catalog.IssuedAt = issued.AddTicks(1_230_000);
            catalog.ExpiresAt = catalog.IssuedAt.AddHours(1);
            catalog.Nodes[0].LastSeen = catalog.IssuedAt;
            break;
        case "frac6":
            catalog.IssuedAt = issued.AddTicks(1_234_560);
            catalog.ExpiresAt = catalog.IssuedAt.AddHours(1);
            catalog.Nodes[0].LastSeen = catalog.IssuedAt;
            break;
        case "trail0":
            catalog.IssuedAt = issued.AddTicks(1_200_000);
            catalog.ExpiresAt = catalog.IssuedAt.AddHours(1);
            catalog.Nodes[0].LastSeen = catalog.IssuedAt;
            break;
        case "unicode":
            catalog.Nodes[0].DisplayName = "Привет";
            catalog.Locations[0].City = "Helsinki—metro";
            break;
        case "ipfamily":
            catalog.Nodes[0].Endpoints[0].IpFamily = "dual";
            break;
        case "empty_profiles":
            catalog.Nodes[0].Endpoints[0].Profiles = Array.Empty<string>();
            break;
        case "nil_profiles":
            catalog.Nodes[0].Endpoints[0].Profiles = null!;
            break;
        case "empty_locs":
            catalog.Locations = Array.Empty<LocationDto>();
            catalog.Nodes = Array.Empty<NodeRegistryEntryDto>();
            break;
        case "prod_ip_server":
            catalog.Nodes[0].ServerName = "46.8.218.27";
            catalog.Nodes[0].ServerVersion = "1.0.1";
            catalog.Nodes[0].Endpoints[0].Host = "46.8.218.27";
            catalog.Nodes[0].Endpoints[0].IpFamily = "ipv4";
            break;
        default:
            throw new ArgumentException("unknown fixture: " + name);
    }

    return catalog;
}
