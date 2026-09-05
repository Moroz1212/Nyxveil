using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeCentralManagementTests : IAsyncLifetime
{
    private readonly ControlPlaneTestFixture _fx = new();

    public Task InitializeAsync() => Task.CompletedTask;

    public async Task DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task TestAdminConfigChangeIncrementsConfigVersion()
    {
        var nodeId = await RegisterManagedNodeAsync();
        var before = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        await _fx.NodeManagement.SetDrainingAsync(nodeId, true, "admin");
        var after = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        Assert.Equal(before.ConfigVersion + 1, after.ConfigVersion);
        Assert.True(after.Draining);
    }

    [Fact]
    public async Task TestGetConfigReturnsAuthoritativeConfig()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetEnabledAsync(nodeId, false, "admin");
        var cfg = await _fx.Nodes.GetConfigAsync(nodeId);
        Assert.False(cfg.Enabled);
        Assert.Equal((await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId)).ConfigVersion, cfg.ConfigVersion);
    }

    [Fact]
    public async Task TestHeartbeatReturnsAuthoritativeConfigVersion()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetCapacityAsync(nodeId, 50, "admin");
        var expected = (await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId)).ConfigVersion;
        var hb = await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 1
        });
        Assert.Equal(expected, hb.ConfigVersion);
    }

    [Fact]
    public async Task TestHeartbeatDoesNotIncrementConfigVersion()
    {
        var nodeId = await RegisterManagedNodeAsync();
        var before = (await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId)).ConfigVersion;
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = nodeId, CurrentSessions = 2 });
        var after = (await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId)).ConfigVersion;
        Assert.Equal(before, after);
    }

    [Fact]
    public async Task TestConfigVersionNotChangedByHeartbeat() => await TestHeartbeatDoesNotIncrementConfigVersion();

    [Fact]
    public async Task TestAdminDisableIsReturnedToNodeConfig()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetEnabledAsync(nodeId, false, "admin");
        Assert.False((await _fx.Nodes.GetConfigAsync(nodeId)).Enabled);
    }

    [Fact]
    public async Task TestAdminDrainingIsReturnedToNodeConfig()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetDrainingAsync(nodeId, true, "admin");
        Assert.True((await _fx.Nodes.GetConfigAsync(nodeId)).Draining);
    }

    [Fact]
    public async Task TestAdminMaintenanceIsReturnedToNodeConfig()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.EnterMaintenanceAsync(nodeId, "admin");
        Assert.True((await _fx.Nodes.GetConfigAsync(nodeId)).MaintenanceMode);
    }

    [Fact]
    public async Task TestAdminCapacityIsReturnedToNodeConfig()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetCapacityAsync(nodeId, 42, "admin");
        Assert.Equal(42, (await _fx.Nodes.GetConfigAsync(nodeId)).Capacity);
    }

    [Fact]
    public async Task TestAdminChangePersistsAcrossDbReload()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetDrainingAsync(nodeId, true, "admin");
        await _fx.Db.Entry(await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId)).ReloadAsync();
        Assert.True((await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId)).Draining);
    }

    [Fact]
    public async Task TestHeartbeatDoesNotOverwriteNodeConfiguration()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetEnabledAsync(nodeId, false, "admin");
        await _fx.NodeManagement.SetDrainingAsync(nodeId, true, "admin");
        await _fx.NodeManagement.EnterMaintenanceAsync(nodeId, "admin");
        await _fx.NodeManagement.SetTestOnlyAsync(nodeId, true, "admin");
        await _fx.NodeManagement.SetCapacityAsync(nodeId, 10, "admin");

        var nodeBefore = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId);
        var cfgBefore = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        var location = nodeBefore.LocationId;
        var display = nodeBefore.DisplayName;
        var identity = nodeBefore.PublicIdentity.ToArray();
        var spki = nodeBefore.SpkiPin!.ToArray();
        var serverVersion = nodeBefore.ServerVersion;
        var serverName = nodeBefore.ServerName;
        var protocol = nodeBefore.ProtocolVersion;
        var cfgVersion = cfgBefore.ConfigVersion;

        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 9,
            Capacity = 999,
            Version = "9.9.9",
            Timestamp = new DateTime(2099, 1, 1, 0, 0, 0, DateTimeKind.Utc),
            Healthy = true
        });

        var node = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId);
        var cfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        Assert.Equal(location, node.LocationId);
        Assert.Equal(display, node.DisplayName);
        Assert.False(node.Enabled);
        Assert.True(node.TestOnly);
        Assert.True(node.Draining);
        Assert.Equal(protocol, node.ProtocolVersion);
        Assert.Equal(serverVersion, node.ServerVersion);
        Assert.Equal(serverName, node.ServerName);
        Assert.True(identity.SequenceEqual(node.PublicIdentity));
        Assert.True(spki.SequenceEqual(node.SpkiPin!));
        Assert.Equal(cfgVersion, cfg.ConfigVersion);
        Assert.True(cfg.MaintenanceMode);
        Assert.False(cfg.Enabled);
        Assert.True(cfg.Draining);
        Assert.Equal(10, cfg.Capacity);
        Assert.Equal(9, node.CurrentSessions);
        Assert.Equal(10, node.Capacity); // capped to configured
    }

    [Fact]
    public async Task TestHeartbeatUsesServerReceiveTime()
    {
        var nodeId = await RegisterManagedNodeAsync();
        var fixedNow = new DateTime(2026, 9, 5, 12, 0, 0, DateTimeKind.Utc);
        _fx.Clock.UtcNow = fixedNow;
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 1,
            Timestamp = new DateTime(2099, 6, 1, 0, 0, 0, DateTimeKind.Utc)
        });
        var node = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal(fixedNow, node.LastSeenAt);
        var health = await _fx.Db.NodeHealth.AsNoTracking().SingleAsync(h => h.NodeId == nodeId);
        Assert.Equal(fixedNow, health.UpdatedAt);
    }

    [Fact]
    public async Task TestFutureNodeTimestampCannotKeepNodeOnline()
    {
        var nodeId = await RegisterManagedNodeAsync();
        _fx.Clock.UtcNow = new DateTime(2026, 1, 1, 0, 0, 0, DateTimeKind.Utc);
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 0,
            Timestamp = new DateTime(2099, 1, 1, 0, 0, 0, DateTimeKind.Utc)
        });
        var lastSeen = (await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId)).LastSeenAt!.Value;
        Assert.True(lastSeen.Year < 2030);
        Assert.Equal(2026, lastSeen.Year);
    }

    [Fact]
    public async Task TestHeartbeatCannotExitMaintenance()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.EnterMaintenanceAsync(nodeId, "admin");
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 1,
            Healthy = true,
            Version = "2.0.0"
        });
        Assert.True((await _fx.Nodes.GetConfigAsync(nodeId)).MaintenanceMode);
        var (token, _, _) = await CreateUserLicenseAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var entry = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == nodeId);
        Assert.True(entry.Draining); // maintenance → draining for Frozen selector
    }

    [Fact]
    public async Task TestMaintenanceNodeNotConnectableByFrozenSelector()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.EnterMaintenanceAsync(nodeId, "admin");
        var (token, _, _) = await CreateUserLicenseAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var entry = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == nodeId);
        Assert.True(entry.Draining || !entry.Enabled);
    }

    [Fact]
    public async Task TestDrainingNodeNotConnectable()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetDrainingAsync(nodeId, true, "admin");
        var (token, _, _) = await CreateUserLicenseAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.True(Assert.Single(signed.Catalog.Nodes, n => n.NodeId == nodeId).Draining);
    }

    [Fact]
    public async Task TestDisabledNodeNotConnectable()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetEnabledAsync(nodeId, false, "admin");
        var (token, _, _) = await CreateUserLicenseAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.DoesNotContain(signed.Catalog.Nodes, n => n.NodeId == nodeId);
    }

    [Fact]
    public async Task TestMasterCanSeeTestOnlyNode()
    {
        await SeedTestNodeAsync();
        var (token, _, _) = await CreateUserLicenseAsync(role: "master", planId: _fx.MasterPlanId);
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.Contains(signed.Catalog.Nodes, n => n.TestOnly);
    }

    [Fact]
    public async Task TestUserCannotSeeTestOnlyNode()
    {
        await SeedTestNodeAsync();
        var (token, _, _) = await CreateUserLicenseAsync(role: "user");
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.DoesNotContain(signed.Catalog.Nodes, n => n.TestOnly);
    }

    [Fact]
    public async Task TestTestRoleCannotSeeTestOnlyNode()
    {
        await SeedTestNodeAsync();
        var (token, _, _) = await CreateUserLicenseAsync(role: "test");
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.DoesNotContain(signed.Catalog.Nodes, n => n.TestOnly);
    }

    [Fact]
    public async Task TestMasterAccessUsesRoleNotPlan()
    {
        var lic = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId, // non-master plan
            Role = "master",
            AllowedLocations = new[] { _fx.LocationId }
        });
        var result = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = lic.LicenseToken
        });
        Assert.True(result.Valid);
        Assert.Equal("master", result.Role);
        Assert.Equal("standard", result.Plan);
    }

    [Fact]
    public async Task TestPlanMasterDoesNotGrantMasterRole()
    {
        var lic = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.MasterPlanId, // plan code "master"
            Role = "user",
            AllowedLocations = new[] { _fx.LocationId }
        });
        var result = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = lic.LicenseToken
        });
        Assert.Equal("user", result.Role);
        Assert.False(string.Equals(result.Role, "master", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task TestRoleMasterOnNonMasterPlanGetsMasterAccess()
    {
        var lic = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "master",
            AllowedLocations = new[] { _fx.LocationId }
        });
        var result = await _fx.Licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = lic.LicenseToken
        });
        Assert.True(result.Valid && string.Equals(result.Role, "master", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task TestInvalidLicenseRoleRejected()
    {
        await Assert.ThrowsAsync<ValidationException>(() => _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "administrator",
            AllowedLocations = new[] { _fx.LocationId }
        }));
    }

    [Fact]
    public async Task TestGetConfigReturnsSameConfigVersion()
    {
        var nodeId = await RegisterManagedNodeAsync();
        await _fx.NodeManagement.SetEnabledAsync(nodeId, true, "admin");
        var cfg = await _fx.Nodes.GetConfigAsync(nodeId);
        var hb = await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = nodeId, CurrentSessions = 0 });
        Assert.Equal(cfg.ConfigVersion, hb.ConfigVersion);
    }

    private async Task<string> RegisterManagedNodeAsync()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            AllowedLocation = _fx.LocationId,
            MaxUses = 1,
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1)
        });
        var nodeId = "node-mgmt-" + Guid.NewGuid().ToString("N")[..8];
        var req = new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            DisplayName = nodeId,
            LocationId = _fx.LocationId,
            ProtocolVersion = 1,
            ServerVersion = "1.0.0",
            ServerName = "vpn.example.test",
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            Capacity = 100,
            Endpoints =
            [
                new NodeEndpointDto
                {
                    Host = "vpn.example.test",
                    Port = 443,
                    AddressFamily = "hostname",
                    Priority = 1,
                    Enabled = true
                }
            ]
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(req);
        return nodeId;
    }

    private async Task SeedTestNodeAsync()
    {
        var now = _fx.Clock.UtcNow;
        if (await _fx.Db.Nodes.AnyAsync(n => n.NodeId == "node-testonly"))
            return;
        _fx.Db.Nodes.Add(new Node
        {
            NodeId = "node-testonly",
            LocationId = _fx.LocationId,
            DisplayName = "TestOnly",
            Enabled = true,
            TestOnly = true,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            ServerName = "test.example.test",
            ProtocolVersion = 1,
            ServerVersion = "1.0.0",
            Capacity = 10,
            Status = NodeRuntimeStatus.Healthy,
            CreatedAt = now,
            UpdatedAt = now,
            ConfigVersion = 1,
            Transports =
            [
                new NodeTransport { Id = Guid.NewGuid(), NodeId = "node-testonly", TransportType = "quic", Enabled = true, Priority = 1 }
            ]
        });
        _fx.Db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = "node-testonly",
            Enabled = true,
            Capacity = 10,
            TransportPolicyJson = "{}",
            ConfigVersion = 1,
            UpdatedAt = now
        });
        await _fx.Db.SaveChangesAsync();
    }

    private async Task<(string Token, string DeviceId, byte[] Pub)> CreateUserLicenseAsync(
        string role = "user",
        Guid? planId = null)
    {
        var lic = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = planId ?? _fx.StandardPlanId,
            Role = role,
            AllowedLocations = new[] { _fx.LocationId },
            MaxDevices = 3
        });
        var deviceId = "dev-" + Guid.NewGuid().ToString("N")[..8];
        var pub = ControlPlaneTestFixture.RandomKey32();
        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = lic.LicenseToken,
            DeviceId = deviceId,
            PublicKey = pub
        });
        return (lic.LicenseToken, deviceId, pub);
    }
}
