using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.CoreInterop.Emit;

/// <summary>
/// Emits production TicketService / CatalogService artifacts for Frozen Core interop.
/// </summary>
public static class Program
{
    public static async Task<int> Main(string[] args)
    {
        if (args.Length < 1)
        {
            Console.Error.WriteLine("usage: CsEmit <issue-ticket|sign-catalog|verify-node-token> ...");
            return 2;
        }

        return args[0] switch
        {
            "issue-ticket" => await IssueTicketAsync(args),
            "sign-catalog" => await SignCatalogAsync(args),
            "verify-node-token" => await VerifyNodeTokenAsync(args),
            _ => Fail("unknown command")
        };
    }

    private static async Task<int> IssueTicketAsync(string[] args)
    {
        var outDir = RequireArg(args, "--out-dir");
        Directory.CreateDirectory(outDir);
        await using var fx = await BuildFixtureAsync();
        await SeedProductionFixtureAsync(fx);

        var issued = await fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = fx.LicenseToken!,
            DeviceId = fx.DeviceId!,
            LocationId = "ams" // admin alias → canonical loc-ams
        });

        var material = await fx.Keys.GetCurrentSigningMaterialAsync();
        var parts = issued.AccessTicket.Split('.');
        var headerJson = Encoding.UTF8.GetString(Base64UrlDecode(parts[0]));
        using var headerDoc = JsonDocument.Parse(headerJson);
        var kid = headerDoc.RootElement.GetProperty("kid").GetString() ?? material.KeyId;

        var claims = fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        if (claims.Locations is null || claims.Locations.Count != 1 ||
            !string.Equals(claims.Locations[0], "loc-ams", StringComparison.Ordinal))
            throw new InvalidOperationException("ticket locations must be canonical [loc-ams], got: " +
                                                string.Join(",", claims.Locations ?? Array.Empty<string>()));

        await File.WriteAllTextAsync(Path.Combine(outDir, "ticket.jwt"), issued.AccessTicket);
        await File.WriteAllTextAsync(Path.Combine(outDir, "ticket.meta.json"), JsonSerializer.Serialize(new
        {
            kid,
            issuer = "nyxveil-control-plane-interop",
            audience = "nvp-node",
            pubkey_hex = Convert.ToHexString(material.PublicKey).ToLowerInvariant(),
            expected_location_id = "loc-ams",
            device_id = fx.DeviceId
        }));
        Console.WriteLine("issued production ticket loc-ams");
        return 0;
    }

    private static async Task<int> SignCatalogAsync(string[] args)
    {
        var outDir = RequireArg(args, "--out-dir");
        Directory.CreateDirectory(outDir);
        await using var fx = await BuildFixtureAsync();
        await SeedProductionFixtureAsync(fx);

        var signed = await fx.Catalog.GetSignedCatalogForCallerAsync(null, fx.LicenseToken);
        if (signed.Catalog.Nodes.Count < 1)
            throw new InvalidOperationException("production catalog must include at least one node");

        var node = signed.Catalog.Nodes.FirstOrDefault(n => n.NodeId == "node-ams-1")
                   ?? throw new InvalidOperationException("missing node-ams-1 in catalog");
        if (!string.Equals(node.LocationId, "loc-ams", StringComparison.Ordinal))
            throw new InvalidOperationException("node location_id must be loc-ams");
        if (node.Endpoints.Count < 1)
            throw new InvalidOperationException("node must have endpoints");
        var profiles = node.Endpoints[0].Profiles ?? Array.Empty<string>();
        if (!profiles.Contains("quic-udp-443") || !profiles.Contains("tls-tcp-443"))
            throw new InvalidOperationException("profiles must include quic-udp-443 and tls-tcp-443");

        var material = await fx.Keys.GetCurrentSigningMaterialAsync();
        var options = new JsonSerializerOptions
        {
            DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull
        };
        options.Converters.Add(new Rfc3339NanoDateTimeConverter());
        var json = JsonSerializer.Serialize(signed, options);
        await File.WriteAllTextAsync(Path.Combine(outDir, "catalog.json"), json);
        await File.WriteAllTextAsync(Path.Combine(outDir, "catalog.meta.json"), JsonSerializer.Serialize(new
        {
            kid = material.KeyId,
            pubkey_hex = Convert.ToHexString(material.PublicKey).ToLowerInvariant(),
            expected_node_id = "node-ams-1",
            expected_location_id = "loc-ams"
        }));

        // TestOnly role semantics artifact for harness.
        var masterSigned = await fx.Catalog.GetSignedCatalogForCallerAsync(null, fx.MasterLicenseToken);
        var userHasTest = signed.Catalog.Nodes.Any(n => n.TestOnly);
        var masterHasTest = masterSigned.Catalog.Nodes.Any(n => n.TestOnly);
        await File.WriteAllTextAsync(Path.Combine(outDir, "testonly.meta.json"), JsonSerializer.Serialize(new
        {
            user_sees_testonly = userHasTest,
            master_sees_testonly = masterHasTest
        }));

        Console.WriteLine("signed production catalog");
        return 0;
    }

    private static async Task<int> VerifyNodeTokenAsync(string[] args)
    {
        var tokenFile = RequireArg(args, "--token-file");
        var nodeId = RequireArg(args, "--node-id");
        var pubHex = RequireArg(args, "--pubkey-hex");
        var token = (await File.ReadAllTextAsync(tokenFile)).Trim();
        var pub = Convert.FromHexString(pubHex);

        await using var fx = await BuildFixtureAsync();
        var now = DateTime.UtcNow;
        fx.Db.Locations.Add(new Location
        {
            LocationId = "loc-ams",
            Code = "ams",
            Country = "NL",
            City = "Amsterdam",
            DisplayName = "Amsterdam",
            CountryCode = "NL",
            Enabled = true,
            CreatedAt = now,
            UpdatedAt = now
        });
        fx.Db.Nodes.Add(new Node
        {
            NodeId = nodeId,
            LocationId = "loc-ams",
            DisplayName = nodeId,
            Enabled = true,
            PublicIdentity = RandomNumberGenerator.GetBytes(32),
            Capacity = 10,
            CreatedAt = now,
            UpdatedAt = now,
            ConfigVersion = 1
        });
        fx.Db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = nodeId,
            Enabled = true,
            Capacity = 10,
            TransportPolicyJson = "{}",
            ConfigVersion = 1,
            UpdatedAt = now
        });
        fx.Db.NodeCredentials.Add(new NodeCredential
        {
            NodeId = nodeId,
            PublicKey = pub,
            CredentialIssuedAt = now
        });
        await fx.Db.SaveChangesAsync();

        await fx.NodeAuth.ValidateNodeRequestAsync(nodeId, new Dictionary<string, string>
        {
            ["Authorization"] = "Bearer " + token
        });
        Console.WriteLine("OK node token verified by C#");
        return 0;
    }

    private static async Task SeedProductionFixtureAsync(EmitFixture fx)
    {
        var now = DateTime.UtcNow;
        var planId = Guid.NewGuid();
        var masterPlanId = Guid.NewGuid();
        fx.Db.Plans.AddRange(
            new Plan
            {
                PlanId = planId,
                Code = "standard",
                Name = "Standard",
                Status = "Active",
                DurationDays = 30,
                MaxDevices = 5,
                AllowedLocationsPolicy = "[]",
                Permissions = """["connect"]""",
                CreatedAt = now,
                UpdatedAt = now
            },
            new Plan
            {
                PlanId = masterPlanId,
                Code = "premium",
                Name = "Premium",
                Status = "Active",
                DurationDays = 365,
                MaxDevices = 10,
                AllowedLocationsPolicy = "*",
                Permissions = """["connect"]""",
                CreatedAt = now,
                UpdatedAt = now
            });

        fx.Db.Locations.Add(new Location
        {
            LocationId = "loc-ams",
            Code = "ams",
            Country = "Netherlands",
            City = "Amsterdam",
            DisplayName = "Amsterdam",
            CountryCode = "NL",
            Enabled = true,
            SortOrder = 1,
            CreatedAt = now,
            UpdatedAt = now
        });
        await fx.Db.SaveChangesAsync();

        var userLic = await fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = planId,
            Role = "user",
            AllowedLocations = new[] { "loc-ams" },
            MaxDevices = 3
        });
        var masterLic = await fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = masterPlanId,
            Role = "master",
            AllowedLocations = new[] { "loc-ams" },
            MaxDevices = 5
        });

        var deviceId = "dev_interop_1";
        var devicePub = RandomNumberGenerator.GetBytes(32);
        await fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = userLic.LicenseToken,
            DeviceId = deviceId,
            PublicKey = devicePub,
            Platform = "interop"
        });

        var spki = RandomNumberGenerator.GetBytes(32);
        var identity = RandomNumberGenerator.GetBytes(32);
        fx.Db.Nodes.Add(new Node
        {
            NodeId = "node-ams-1",
            LocationId = "loc-ams",
            DisplayName = "Amsterdam-1",
            Enabled = true,
            TestOnly = false,
            Draining = false,
            ProtocolVersion = 1,
            ServerVersion = "1.0.0",
            ServerName = "vpn.example.test",
            SpkiPin = spki,
            PublicIdentity = identity,
            Capacity = 100,
            CurrentSessions = 3,
            LastSeenAt = now,
            Status = NodeRuntimeStatus.Healthy,
            CreatedAt = now,
            UpdatedAt = now,
            ConfigVersion = 1,
            Endpoints =
            [
                new NodeEndpoint
                {
                    Id = Guid.NewGuid(),
                    NodeId = "node-ams-1",
                    Host = "vpn.example.test",
                    Port = 443,
                    AddressFamily = "hostname",
                    Priority = 1,
                    Enabled = true
                }
            ],
            Transports =
            [
                new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-ams-1", TransportType = "quic", Enabled = true, Priority = 1 },
                new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-ams-1", TransportType = "tls", Enabled = true, Priority = 2 }
            ]
        });
        fx.Db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = "node-ams-1",
            Enabled = true,
            Draining = false,
            MaintenanceMode = false,
            Capacity = 100,
            TransportPolicyJson = "{}",
            ConfigVersion = 1,
            UpdatedAt = now
        });
        fx.Db.NodeHealth.Add(new NodeHealth
        {
            NodeId = "node-ams-1",
            Healthy = true,
            ActiveSessions = 3,
            CpuPercent = 10,
            MemoryPercent = 20,
            UpdatedAt = now
        });

        // Test-only node for role visibility checks.
        fx.Db.Nodes.Add(new Node
        {
            NodeId = "node-test-1",
            LocationId = "loc-ams",
            DisplayName = "Test-1",
            Enabled = true,
            TestOnly = true,
            PublicIdentity = RandomNumberGenerator.GetBytes(32),
            SpkiPin = RandomNumberGenerator.GetBytes(32),
            ServerName = "test.example.test",
            ProtocolVersion = 1,
            ServerVersion = "1.0.0",
            Capacity = 10,
            Status = NodeRuntimeStatus.Healthy,
            LastSeenAt = now,
            CreatedAt = now,
            UpdatedAt = now,
            ConfigVersion = 1,
            Endpoints =
            [
                new NodeEndpoint
                {
                    Id = Guid.NewGuid(),
                    NodeId = "node-test-1",
                    Host = "test.example.test",
                    Port = 443,
                    AddressFamily = "hostname",
                    Priority = 1,
                    Enabled = true
                }
            ],
            Transports =
            [
                new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-test-1", TransportType = "quic", Enabled = true, Priority = 1 }
            ]
        });
        fx.Db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = "node-test-1",
            Enabled = true,
            Capacity = 10,
            TransportPolicyJson = "{}",
            ConfigVersion = 1,
            UpdatedAt = now
        });
        await fx.Db.SaveChangesAsync();

        fx.LicenseToken = userLic.LicenseToken;
        fx.MasterLicenseToken = masterLic.LicenseToken;
        fx.DeviceId = deviceId;
    }

    private static byte[] Base64UrlDecode(string input)
    {
        var s = input.Replace('-', '+').Replace('_', '/');
        switch (s.Length % 4)
        {
            case 2: s += "=="; break;
            case 3: s += "="; break;
        }
        return Convert.FromBase64String(s);
    }

    private static string RequireArg(string[] args, string name)
    {
        for (var i = 0; i < args.Length - 1; i++)
            if (args[i] == name)
                return args[i + 1];
        throw new ArgumentException($"missing {name}");
    }

    private static int Fail(string msg)
    {
        Console.Error.WriteLine(msg);
        return 1;
    }

    private static async Task<EmitFixture> BuildFixtureAsync()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddDataProtection();
        services.AddSingleton<IClock, SystemClock>();
        services.AddSingleton(Options.Create(new SecurityOptions
        {
            LicenseKekHex = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant()
        }));
        services.AddSingleton(Options.Create(new SigningOptions
        {
            Issuer = "nyxveil-control-plane-interop",
            Audience = "nvp-node"
        }));
        services.AddSingleton(Options.Create(new TicketOptions { TtlMinutes = 15 }));
        services.AddSingleton(Options.Create(new NodeAuthOptions { AllowLegacyBearer = false }));
        var dbName = "interop-" + Guid.NewGuid().ToString("N");
        services.AddDbContext<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase(dbName)
                .ConfigureWarnings(w => w.Ignore(
                    Microsoft.EntityFrameworkCore.Diagnostics.InMemoryEventId.TransactionIgnoredWarning)));
        services.AddSingleton<ILicenseKeyHasher, LicenseKeyHasher>();
        services.AddSingleton<ISigningKeyService, Ed25519SigningKeyStore>();
        services.AddSingleton<ICatalogSigner, CatalogSigner>();
        services.AddScoped<AccessTicketService>();
        services.AddScoped<IAccessTicketIssuer>(sp => sp.GetRequiredService<AccessTicketService>());
        services.AddScoped<NodeAuthService>();
        services.AddScoped<INodeAuthenticator>(sp => sp.GetRequiredService<NodeAuthService>());
        services.AddScoped<ILicenseProvisioningService, LicenseProvisioningService>();
        services.AddScoped<IDeviceService, DeviceService>();
        services.AddScoped<ITicketService, TicketService>();
        services.AddScoped<ICatalogService, CatalogService>();
        services.AddScoped<IAuditService, AuditService>();
        services.AddScoped<INodeManagementService, NodeManagementService>();

        var sp = services.BuildServiceProvider();
        var scope = sp.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        await db.Database.EnsureCreatedAsync();
        return new EmitFixture(sp, scope);
    }

    private sealed class SystemClock : IClock
    {
        public DateTime UtcNow => DateTime.UtcNow;
    }

    private sealed class EmitFixture : IAsyncDisposable
    {
        private readonly ServiceProvider _sp;
        public IServiceScope Scope { get; }
        public ControlPlaneDbContext Db { get; }
        public ISigningKeyService Keys { get; }
        public AccessTicketService TicketIssuer { get; }
        public ITicketService Tickets { get; }
        public ICatalogService Catalog { get; }
        public ILicenseProvisioningService Licenses { get; }
        public IDeviceService Devices { get; }
        public NodeAuthService NodeAuth { get; }
        public string? LicenseToken { get; set; }
        public string? MasterLicenseToken { get; set; }
        public string? DeviceId { get; set; }

        public EmitFixture(ServiceProvider sp, IServiceScope scope)
        {
            _sp = sp;
            Scope = scope;
            Db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
            Keys = scope.ServiceProvider.GetRequiredService<ISigningKeyService>();
            TicketIssuer = scope.ServiceProvider.GetRequiredService<AccessTicketService>();
            Tickets = scope.ServiceProvider.GetRequiredService<ITicketService>();
            Catalog = scope.ServiceProvider.GetRequiredService<ICatalogService>();
            Licenses = scope.ServiceProvider.GetRequiredService<ILicenseProvisioningService>();
            Devices = scope.ServiceProvider.GetRequiredService<IDeviceService>();
            NodeAuth = scope.ServiceProvider.GetRequiredService<NodeAuthService>();
        }

        public ValueTask DisposeAsync()
        {
            Scope.Dispose();
            _sp.Dispose();
            return ValueTask.CompletedTask;
        }
    }
}
