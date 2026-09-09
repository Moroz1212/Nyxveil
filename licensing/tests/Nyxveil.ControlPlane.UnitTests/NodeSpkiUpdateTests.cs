using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeSpkiUpdateTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public ValueTask DisposeAsync() => _fx.DisposeAsync();

    [Fact]
    public async Task UpdateSpki_UpdatesOnlyPin_PreservesAdminAndIdentity()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            MaxUses = 1,
            ExpiresAt = DateTime.UtcNow.AddHours(1),
            AllowedLocation = _fx.LocationId,
            CreatedBy = "test"
        });
        var identity = ControlPlaneTestFixture.RandomKey32();
        var pub = ControlPlaneTestFixture.RandomKey32();
        var oldPin = ControlPlaneTestFixture.RandomKey32();
        var nodeId = "spki-node-" + Guid.NewGuid().ToString("N")[..8];
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = "spki",
            PublicIdentity = identity,
            PublicKey = pub,
            ServerName = "spki.example.test",
            SpkiPin = oldPin,
            Capacity = 50,
            ProtocolVersion = 1,
            ServerVersion = "1.1.11"
        });

        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        node.Enabled = true;
        node.Draining = true;
        node.TestOnly = true;
        await _fx.Db.SaveChangesAsync();

        var cfg = await _fx.Db.NodeConfigs.SingleAsync(c => c.NodeId == nodeId);
        cfg.MaintenanceMode = true;
        await _fx.Db.SaveChangesAsync();

        var newPin = ControlPlaneTestFixture.RandomKey32();
        var resp = await _fx.Nodes.UpdateSpkiAsync(nodeId, newPin);

        Assert.Equal(nodeId, resp.NodeId);
        Assert.Equal(newPin, resp.SpkiPin);

        var after = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal(newPin, after.SpkiPin);
        Assert.Equal(identity, after.PublicIdentity);
        Assert.Equal(_fx.LocationId, after.LocationId);
        Assert.True(after.Enabled);
        Assert.True(after.Draining);
        Assert.True(after.TestOnly);
        Assert.Equal(50, after.Capacity);
        Assert.Equal("spki.example.test", after.ServerName);

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        Assert.True(afterCfg.MaintenanceMode);

        var cred = await _fx.Db.NodeCredentials.AsNoTracking().SingleAsync(c => c.NodeId == nodeId);
        Assert.Equal(pub, cred.PublicKey);
    }

    [Fact]
    public async Task UpdateSpki_RejectsInvalidPinLength()
    {
        await Assert.ThrowsAsync<ValidationException>(() =>
            _fx.Nodes.UpdateSpkiAsync("missing", new byte[16]));
    }

    [Fact]
    public async Task Register_AlwaysRequiresBootstrap_EmptyRejected()
    {
        await Assert.ThrowsAsync<UnauthorizedException>(() =>
            _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
            {
                BootstrapToken = "",
                NodeId = "no-boot",
                LocationId = _fx.LocationId,
                PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
                PublicKey = ControlPlaneTestFixture.RandomKey32(),
                SpkiPin = ControlPlaneTestFixture.RandomKey32()
            }));
    }

    [Fact]
    public async Task Register_FreshWithSpki_StoresPin()
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            MaxUses = 1,
            ExpiresAt = DateTime.UtcNow.AddHours(1),
            AllowedLocation = _fx.LocationId,
            CreatedBy = "test"
        });
        var pin = ControlPlaneTestFixture.RandomKey32();
        var nodeId = "fresh-spki-" + Guid.NewGuid().ToString("N")[..8];
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            SpkiPin = pin,
            Capacity = 10
        });
        var node = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal(pin, node.SpkiPin);
    }
}
