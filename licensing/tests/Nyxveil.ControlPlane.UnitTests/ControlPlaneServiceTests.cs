using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Tickets;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class ControlPlaneServiceTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public ValueTask DisposeAsync() => _fx.DisposeAsync();

    [Fact]
    public async Task TestCreateLicense()
    {
        var created = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "user",
            MaxDevices = 2,
            AllowedLocations = new[] { _fx.LocationId },
            CreatedBy = "test"
        });

        Assert.StartsWith("nyx_lic_", created.LicenseId);
        Assert.Contains(':', created.LicenseToken);
        Assert.Equal(created.LicenseId, created.LicenseToken.Split(':')[0]);

        // Raw license shown once — only verifier is persisted.
        var stored = await _fx.Db.Licenses.SingleAsync();
        Assert.DoesNotContain(created.LicenseToken.Split(':')[1], stored.LicenseKeyVerifier);
        Assert.StartsWith("hmac1:", stored.LicenseKeyVerifier);
        Assert.True(_fx.Hasher.Verify(stored.LicenseKeyVerifier, created.LicenseToken.Split(':')[1]));
    }

    [Fact]
    public async Task TestLicenseVerifierWorks()
    {
        var created = await CreateStandardLicenseAsync();
        var validated = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = created.LicenseToken
        });

        Assert.True(validated.Valid);
        Assert.Equal(created.LicenseId, validated.LicenseId);
        Assert.Equal("standard", validated.Plan);

        var bad = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = created.LicenseId + ":not-the-secret"
        });
        Assert.False(bad.Valid);
    }

    [Fact]
    public async Task TestExpiredLicenseRejected()
    {
        var created = await CreateStandardLicenseAsync();
        var lic = await _fx.Db.Licenses.SingleAsync();
        lic.ExpiresAt = DateTime.UtcNow.AddMinutes(-5);
        lic.Status = LicenseStatus.Expired;
        await _fx.Db.SaveChangesAsync();

        var validated = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = created.LicenseToken
        });
        Assert.False(validated.Valid);
        Assert.Contains("expired", validated.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task TestDisabledLicenseRejected()
    {
        var created = await CreateStandardLicenseAsync();
        var lic = await _fx.Db.Licenses.SingleAsync();
        await _fx.Licenses.DisableLicenseAsync(lic.LicenseId);

        var validated = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = created.LicenseToken
        });
        Assert.False(validated.Valid);
        Assert.Contains("disabled", validated.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task TestRevokedLicenseRejected()
    {
        var created = await CreateStandardLicenseAsync();
        var lic = await _fx.Db.Licenses.SingleAsync();
        await _fx.Licenses.RevokeLicenseAsync(lic.LicenseId);

        var validated = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = created.LicenseToken
        });
        Assert.False(validated.Valid);
        Assert.Contains("revoked", validated.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task TestExtendLicense()
    {
        var created = await CreateStandardLicenseAsync();
        var lic = await _fx.Db.Licenses.SingleAsync();
        lic.Status = LicenseStatus.Expired;
        lic.ExpiresAt = DateTime.UtcNow.AddDays(-1);
        await _fx.Db.SaveChangesAsync();

        var newExpiry = DateTime.UtcNow.AddDays(60);
        await _fx.Licenses.ExtendLicenseAsync(new ExtendLicenseRequest
        {
            LicenseId = lic.LicenseId,
            ExpiresAt = newExpiry
        });

        await _fx.Db.Entry(lic).ReloadAsync();
        Assert.Equal(LicenseStatus.Active, lic.Status);
        Assert.True(lic.ExpiresAt > DateTime.UtcNow.AddDays(50));
    }

    [Fact]
    public async Task TestActivateDevice()
    {
        var created = await CreateStandardLicenseAsync();
        var deviceId = "dev-1";
        var pk = ControlPlaneTestFixture.RandomKey32();

        var result = await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = pk,
            Platform = "windows"
        });

        Assert.True(result.Activated);
        Assert.Equal(deviceId, result.DeviceId);
        Assert.Equal(1, await _fx.Db.Devices.CountAsync());
    }

    [Fact]
    public async Task TestDeviceActivationIdempotent()
    {
        var created = await CreateStandardLicenseAsync();
        var deviceId = "dev-idem";
        var pk = ControlPlaneTestFixture.RandomKey32();
        var req = new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = pk
        };

        await _fx.Devices.ActivateAsync(req);
        var again = await _fx.Devices.ActivateAsync(req);

        Assert.True(again.Activated);
        Assert.Equal(1, await _fx.Db.Devices.CountAsync());
    }

    [Fact]
    public async Task TestMaxDeviceLimit()
    {
        var created = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            MaxDevices = 1,
            AllowedLocations = new[] { _fx.LocationId },
            CreatedBy = "test"
        });

        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = "dev-a",
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        });

        await Assert.ThrowsAsync<ConflictException>(() => _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = "dev-b",
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        }));
    }

    [Fact]
    public async Task TestRevokedDeviceRejected()
    {
        var created = await CreateStandardLicenseAsync();
        var deviceId = "dev-revoked";
        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        });
        await _fx.Devices.RevokeAsync(deviceId);

        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        }));
    }

    [Fact]
    public async Task TestBootstrapTokenRegistration()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });

        var identity = ControlPlaneTestFixture.RandomKey32();
        var pk = ControlPlaneTestFixture.RandomKey32();
        var registered = await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = "node-1",
            LocationId = _fx.LocationId,
            DisplayName = "Node 1",
            PublicIdentity = identity,
            PublicKey = pk,
            Endpoints =
            [
                new NodeEndpointDto { Host = "node1.example", Port = 443, Priority = 1 }
            ]
        });

        Assert.True(registered.Registered);
        Assert.Equal("node-1", registered.NodeId);
        Assert.StartsWith("nvpnode_node-1_", registered.NodeToken);
        Assert.NotNull(registered.Config);
    }

    [Fact]
    public async Task TestExpiredBootstrapRejected()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });

        var entity = await _fx.Db.BootstrapTokens.SingleAsync();
        entity.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();

        await Assert.ThrowsAsync<UnauthorizedException>(() => _fx.Nodes.RegisterWithBootstrapAsync(
            NewNodeRequest(boot.BootstrapToken, "node-expired")));
    }

    [Fact]
    public async Task TestBootstrapMaxUses()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });

        await _fx.Nodes.RegisterWithBootstrapAsync(NewNodeRequest(boot.BootstrapToken, "node-first"));

        await Assert.ThrowsAsync<UnauthorizedException>(() => _fx.Nodes.RegisterWithBootstrapAsync(
            NewNodeRequest(boot.BootstrapToken, "node-second")));
    }

    [Fact]
    public async Task TestNodeRegistrationIdempotent()
    {
        var boot = await CreateBootstrapAsync(maxUses: 5);
        var identity = ControlPlaneTestFixture.RandomKey32();
        var pk = ControlPlaneTestFixture.RandomKey32();
        var req = NewNodeRequest(boot.BootstrapToken, "node-idem");
        req.PublicIdentity = identity;
        req.PublicKey = pk;

        var first = await _fx.Nodes.RegisterWithBootstrapAsync(req);
        Assert.False(string.IsNullOrWhiteSpace(first.NodeToken));

        var retry = NewNodeRequest(boot.BootstrapToken, "node-idem");
        retry.PublicIdentity = identity;
        retry.PublicKey = pk;
        retry.NodeToken = first.NodeToken;
        var second = await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        Assert.True(first.Registered);
        Assert.True(second.Registered);
        Assert.Equal(string.Empty, second.NodeToken);
        Assert.Equal(1, await _fx.Db.Nodes.CountAsync());
        Assert.Equal(1, (await _fx.Db.BootstrapTokens.SingleAsync()).UsedCount);
    }

    [Fact]
    public async Task TestNodeHeartbeatUpdatesOnlyHealth()
    {
        var (nodeId, token, identity, spki) = await RegisterNodeAsync("node-hb");
        var node = await _fx.Db.Nodes.Include(n => n.Endpoints).SingleAsync(n => n.NodeId == nodeId);
        var endpointHost = node.Endpoints.First().Host;
        var serverVersionBefore = node.ServerVersion;
        var statusBefore = node.Status;
        var cfgVersionBefore = (await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId)).ConfigVersion;

        await _fx.NodeAuth.ValidateNodeRequestAsync(
            nodeId,
            new Dictionary<string, string> { ["Authorization"] = "Bearer " + token });

        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 7,
            Capacity = 200,
            Version = "1.2.3",
            CpuUsage = 42,
            MemoryUsage = 55,
            Healthy = true
        });

        await _fx.Db.Entry(node).ReloadAsync();
        Assert.Equal(7, node.CurrentSessions);
        // Capacity capped to NodeConfig.Capacity (registration default = node capacity).
        Assert.True(node.Capacity <= 200);
        Assert.Equal(serverVersionBefore, node.ServerVersion); // static — not updated by heartbeat
        Assert.Equal(statusBefore, node.Status); // runtime status left to health worker
        Assert.Equal(cfgVersionBefore, (await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId)).ConfigVersion);
        Assert.True(identity.SequenceEqual(node.PublicIdentity));
        Assert.True(spki!.SequenceEqual(node.SpkiPin!));

        await _fx.Db.Entry(node).Collection(n => n.Endpoints).LoadAsync();
        Assert.Equal(endpointHost, node.Endpoints.First().Host);
    }

    [Fact]
    public async Task TestUnknownNodeHeartbeatRejected()
    {
        await Assert.ThrowsAsync<NotFoundException>(() => _fx.Heartbeats.ProcessHeartbeatAsync(
            new NodeHeartbeatRequest
            {
                NodeId = "unknown-node",
                CurrentSessions = 1
            }));
    }

    [Fact]
    public async Task TestNodeCannotImpersonateAnotherNode()
    {
        var (nodeA, tokenA, _, _) = await RegisterNodeAsync("node-a");
        var boot = await CreateBootstrapAsync(maxUses: 2);
        await _fx.Nodes.RegisterWithBootstrapAsync(NewNodeRequest(boot.BootstrapToken, "node-b"));

        await Assert.ThrowsAsync<UnauthorizedException>(() => _fx.NodeAuth.ValidateNodeRequestAsync(
            "node-b",
            new Dictionary<string, string> { ["Authorization"] = "Bearer " + tokenA }));

        Assert.Equal("node-a", nodeA);
    }

    [Fact]
    public async Task TestIssueLocationScopedTicket()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationId, _fx.LocationIdB });
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = _fx.LocationId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId }, claims.Locations);
        Assert.True(claims.NodeScope is null || claims.NodeScope.Count == 0);
    }

    [Fact]
    public async Task TestTicketContainsConnectPermission()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Contains(TicketScopeCalculator.PermissionConnect, claims.Permissions);
        Assert.True(claims.HasPermission(TicketScopeCalculator.PermissionConnect));
    }

    [Fact]
    public async Task TestNodeScopeIntersection()
    {
        var (nodeId, _, _, _) = await RegisterNodeAsync("node-scope");
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationId });

        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            NodeId = nodeId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { nodeId }, claims.NodeScope);

        // Pure calculator intersection used by refresh path.
        var narrowed = TicketScopeCalculator.RefreshNodeScope(
            new[] { nodeId, "other-node" },
            new[] { nodeId });
        Assert.Equal(new[] { nodeId }, narrowed);
    }

    [Fact]
    public async Task TestTicketRefreshNeverWidens()
    {
        var (token, deviceId, licId) = await CreateLicensedDeviceAsync(
            locations: new[] { _fx.LocationId, _fx.LocationIdB });

        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = _fx.LocationId
        });

        // Narrow license allowlist so refresh cannot widen beyond loc-ams.
        var allowed = await _fx.Db.LicenseAllowedLocations
            .Where(a => a.LicenseId == licId)
            .ToListAsync();
        _fx.Db.LicenseAllowedLocations.RemoveRange(allowed);
        _fx.Db.LicenseAllowedLocations.Add(new LicenseAllowedLocation
        {
            LicenseId = licId,
            LocationId = _fx.LocationId
        });
        await _fx.Db.SaveChangesAsync();

        var refreshed = await _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(refreshed.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId }, claims.Locations);
        Assert.DoesNotContain(_fx.LocationIdB, claims.Locations ?? Array.Empty<string>());
    }

    [Fact]
    public async Task TestTicketRefreshLicenseDowngrade()
    {
        var (token, deviceId, licId) = await CreateLicensedDeviceAsync(role: "master", planId: _fx.MasterPlanId);
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });
        Assert.Equal("master", _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket).Role);

        var lic = await _fx.Db.Licenses.Include(l => l.Plan).SingleAsync(l => l.LicenseId == licId);
        lic.Role = "user";
        lic.PlanId = _fx.StandardPlanId;
        await _fx.Db.SaveChangesAsync();

        // Clear tracked plan so reload picks standard.
        _fx.Db.ChangeTracker.Clear();

        var refreshed = await _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        });

        Assert.Equal("user", _fx.TicketIssuer.VerifyAccessTicket(refreshed.AccessTicket).Role);
        Assert.Equal("standard", _fx.TicketIssuer.VerifyAccessTicket(refreshed.AccessTicket).Plan);
    }

    [Fact]
    public async Task TestExpiredLicenseCannotRefresh()
    {
        var (token, deviceId, licId) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        var lic = await _fx.Db.Licenses.SingleAsync(l => l.LicenseId == licId);
        lic.ExpiresAt = DateTime.UtcNow.AddMinutes(-1);
        lic.Status = LicenseStatus.Expired;
        await _fx.Db.SaveChangesAsync();

        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        }));
    }

    [Fact]
    public async Task TestRevokedDeviceCannotRefresh()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        await _fx.Devices.RevokeAsync(deviceId);

        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        }));
    }

    [Fact]
    public async Task TestCatalogSigned()
    {
        var (token, _, _) = await CreateLicensedDeviceAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);

        Assert.False(string.IsNullOrWhiteSpace(signed.KeyId));
        Assert.NotEmpty(signed.Signature);
        Assert.False(string.IsNullOrWhiteSpace(signed.Catalog.Version));
        Assert.NotEmpty(signed.Catalog.Locations);
    }

    [Fact]
    public async Task TestCatalogSignatureVerifiable()
    {
        var signer = _fx.Scope.ServiceProvider.GetRequiredService<ICatalogSigner>();
        var keys = _fx.Scope.ServiceProvider.GetRequiredService<ISigningKeyService>();

        var catalog = new CatalogDto
        {
            Version = "1",
            Locations =
            [
                new LocationDto
                {
                    LocationId = _fx.LocationId,
                    Country = "Netherlands",
                    CountryCode = "NL",
                    City = "Amsterdam",
                    DisplayName = "Amsterdam",
                    Enabled = true
                }
            ],
            Nodes = []
        };

        var signed = await signer.SignAsync(catalog);
        var material = await keys.GetCurrentSigningMaterialAsync();

        Assert.Equal(material.KeyId, signed.KeyId);
        Assert.NotEmpty(signed.Signature);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(signed.Catalog);
        Assert.True(Ed25519SigningKeyStore.Verify(material.PublicKey, payload, signed.Signature));

        var tampered = payload.ToArray();
        tampered[0] ^= 0xFF;
        Assert.False(Ed25519SigningKeyStore.Verify(material.PublicKey, tampered, signed.Signature));
    }

    [Fact]
    public async Task TestUserCannotSeeTestNodes()
    {
        await SeedTestAndProdNodesAsync();
        var (token, _, _) = await CreateLicensedDeviceAsync(role: "user");

        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.DoesNotContain(signed.Catalog.Nodes, n => n.TestOnly);
        Assert.Contains(signed.Catalog.Nodes, n => n.NodeId == "node-prod");
    }

    [Fact]
    public async Task TestMasterCanSeeTestNodes()
    {
        await SeedTestAndProdNodesAsync();
        var (token, _, _) = await CreateLicensedDeviceAsync(role: "master", planId: _fx.MasterPlanId);

        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.Contains(signed.Catalog.Nodes, n => n.TestOnly && n.NodeId == "node-test");
        Assert.Contains(signed.Catalog.Nodes, n => n.NodeId == "node-prod");
    }

    [Fact(Skip = "Anonymous catalog is enforced at API level (LicenseAuth → 401).")]
    public void TestAnonymousCatalogIsApiLevel()
    {
    }

    private async Task<CreateLicenseResponse> CreateStandardLicenseAsync(
        string role = "user",
        Guid? planId = null,
        IReadOnlyList<string>? locations = null,
        int maxDevices = 3)
    {
        return await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = planId ?? _fx.StandardPlanId,
            Role = role,
            MaxDevices = maxDevices,
            AllowedLocations = locations ?? new[] { _fx.LocationId },
            CreatedBy = "test"
        });
    }

    private async Task<(string Token, string DeviceId, Guid LicenseId)> CreateLicensedDeviceAsync(
        string role = "user",
        Guid? planId = null,
        IReadOnlyList<string>? locations = null)
    {
        var created = await CreateStandardLicenseAsync(role, planId, locations);
        var deviceId = "dev-" + Guid.NewGuid().ToString("N")[..8];
        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        });

        Assert.True(Application.Common.LicenseIdFormat.TryParse(created.LicenseId, out var licenseId));
        return (created.LicenseToken, deviceId, licenseId);
    }

    private async Task<CreateBootstrapTokenResponse> CreateBootstrapAsync(int maxUses = 1) =>
        await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = maxUses,
            CreatedBy = "test"
        });

    private NodeRegisterRequest NewNodeRequest(string bootstrapToken, string nodeId) => new()
    {
        BootstrapToken = bootstrapToken,
        NodeId = nodeId,
        LocationId = _fx.LocationId,
        DisplayName = nodeId,
        PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
        PublicKey = ControlPlaneTestFixture.RandomKey32(),
        SpkiPin = ControlPlaneTestFixture.RandomKey32(),
        Endpoints =
        [
            new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1 }
        ]
    };

    private async Task<(string NodeId, string Token, byte[] Identity, byte[]? Spki)> RegisterNodeAsync(string nodeId)
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, nodeId);
        var registered = await _fx.Nodes.RegisterWithBootstrapAsync(req);
        return (registered.NodeId, registered.NodeToken, req.PublicIdentity, req.SpkiPin);
    }

    private async Task SeedTestAndProdNodesAsync()
    {
        var now = _fx.Clock.UtcNow;
        if (!await _fx.Db.Nodes.AnyAsync(n => n.NodeId == "node-prod"))
        {
            _fx.Db.Nodes.Add(new Node
            {
                NodeId = "node-prod",
                LocationId = _fx.LocationId,
                DisplayName = "Prod",
                Enabled = true,
                TestOnly = false,
                PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
                Capacity = 100,
                CreatedAt = now,
                UpdatedAt = now,
                ConfigVersion = 1,
                Status = NodeRuntimeStatus.Healthy,
                Transports =
                [
                    new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-prod", TransportType = "quic", Enabled = true, Priority = 1 },
                    new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-prod", TransportType = "tls", Enabled = true, Priority = 2 }
                ]
            });
        }

        if (!await _fx.Db.Nodes.AnyAsync(n => n.NodeId == "node-test"))
        {
            _fx.Db.Nodes.Add(new Node
            {
                NodeId = "node-test",
                LocationId = _fx.LocationId,
                DisplayName = "Test",
                Enabled = true,
                TestOnly = true,
                PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
                Capacity = 10,
                CreatedAt = now,
                UpdatedAt = now,
                ConfigVersion = 1,
                Status = NodeRuntimeStatus.Healthy,
                Transports =
                [
                    new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-test", TransportType = "quic", Enabled = true, Priority = 1 },
                    new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-test", TransportType = "tls", Enabled = true, Priority = 2 }
                ]
            });
        }

        await _fx.Db.SaveChangesAsync();
        // Ensure NodeConfig rows exist for catalog authority.
        foreach (var id in new[] { "node-prod", "node-test" })
        {
            if (!await _fx.Db.NodeConfigs.AnyAsync(c => c.NodeId == id))
            {
                var n = await _fx.Db.Nodes.SingleAsync(x => x.NodeId == id);
                _fx.Db.NodeConfigs.Add(new NodeConfig
                {
                    NodeId = id,
                    Enabled = n.Enabled,
                    Draining = n.Draining,
                    Capacity = n.Capacity,
                    TransportPolicyJson = "{}",
                    ConfigVersion = 1,
                    UpdatedAt = now
                });
            }
        }
        await _fx.Db.SaveChangesAsync();
    }
}