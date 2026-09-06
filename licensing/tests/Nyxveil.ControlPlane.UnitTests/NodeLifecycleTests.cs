using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeLifecycleTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task DeleteWithSessionsRequiresDrain()
    {
        var request = await RegisterAsync("life-sessions");
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = request.NodeId, CurrentSessions = 2 });

        await Assert.ThrowsAsync<ValidationException>(
            () => _fx.NodeManagement.SoftDeleteAsync(request.NodeId, "operator", "retire"));
        Assert.Equal(NodeLifecycleState.Active, (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == request.NodeId)).LifecycleState);
    }

    [Fact]
    public async Task SoftDeleteDisablesDrainsAndCreatesAudit()
    {
        var request = await RegisterAsync("life-delete");
        await _fx.NodeManagement.SoftDeleteAsync(request.NodeId, "operator", "retire");

        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == request.NodeId);
        Assert.Equal(NodeLifecycleState.Deleted, node.LifecycleState);
        Assert.False(node.Enabled);
        Assert.True(node.Draining);
        Assert.NotNull(node.DeletedAt);
        Assert.True(await _fx.Db.AuditLog.AnyAsync(a => a.EntityId == request.NodeId && a.Action == "node.deleted"));
    }

    [Fact]
    public async Task DeletedNodeCannotReregisterOrHeartbeat()
    {
        var request = await RegisterAsync("life-no-resurrect");
        await _fx.NodeManagement.SoftDeleteAsync(request.NodeId, "operator", null);

        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(request));
        await Assert.ThrowsAsync<ForbiddenException>(
            () => _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = request.NodeId }));
    }

    [Fact]
    public async Task CatalogExcludesDeletedNode()
    {
        var request = await RegisterAsync("life-catalog");
        var license = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "user",
            MaxDevices = 1,
            AllowedLocations = [_fx.LocationId],
            CreatedBy = "test"
        });
        await _fx.NodeManagement.SoftDeleteAsync(request.NodeId, "operator", null);

        var catalog = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, license.LicenseToken);
        Assert.DoesNotContain(catalog.Catalog.Nodes, n => n.NodeId == request.NodeId);
    }

    [Fact]
    public async Task LocationDeleteBlockedByNonDeletedNode()
    {
        await RegisterAsync("life-location");
        await Assert.ThrowsAsync<ValidationException>(
            () => _fx.LocationManagement.DeleteLocationAsync(_fx.LocationId, "operator"));
    }

    [Fact]
    public async Task HeartbeatPersistsCertificateAndRuntimeAdvertisement()
    {
        var request = await RegisterAsync("life-heartbeat-meta");
        var expires = _fx.Clock.UtcNow.AddDays(20);
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = request.NodeId,
            CertSubject = "CN=node.example",
            CertNotAfter = expires,
            CertThumbprint = "AABB",
            AcmeAutoRenew = true,
            TunReady = true,
            TlsOk = true,
            QuicOk = false,
            CpConnected = true
        });

        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == request.NodeId);
        var health = await _fx.Db.NodeHealth.SingleAsync(h => h.NodeId == request.NodeId);
        Assert.Equal("CN=node.example", node.CertSubject);
        Assert.Equal(expires, node.CertNotAfter);
        Assert.True(node.AcmeAutoRenew);
        Assert.True(health.TunReady);
        Assert.False(health.QuicOk);
        Assert.True(health.CpConnected);
    }

    private async Task<NodeRegisterRequest> RegisterAsync(string nodeId)
    {
        var bootstrap = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });
        var request = new NodeRegisterRequest
        {
            BootstrapToken = bootstrap.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            ServerVersion = "1.1.0",
            Capacity = 10,
            Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Enabled = true }]
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(request);
        return request;
    }
}
