using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeCommandLocationSafetyTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly NodeCommandService _commands;

    public NodeCommandLocationSafetyTests()
    {
        _commands = new NodeCommandService(
            _fx.Db,
            _fx.Clock,
            _fx.Scope.ServiceProvider.GetRequiredService<IAuditService>(),
            new FakeServerReleaseService
            {
                Info = new ServerReleaseInfo
                {
                    LatestVersion = "1.1.11",
                    ReleaseTag = "server-v1.1.11",
                    SourceStatus = "ok",
                    LastCheckedAt = DateTimeOffset.UtcNow
                }
            });
    }

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task LastHealthyNode_UpdateBlocked()
    {
        var a = await RegisterHealthyAsync("loc-safe-a");
        await Assert.ThrowsAsync<ConflictException>(() => _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]));
    }

    [Fact]
    public async Task TwoHealthySiblings_UpdateOneAllowed()
    {
        var a = await RegisterHealthyAsync("loc-safe-b1");
        var b = await RegisterHealthyAsync("loc-safe-b2");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        Assert.Equal("1.1.11", cmd.TargetVersion);
        Assert.Equal(NodeCommandStatus.Pending, cmd.Status);
    }

    [Fact]
    public async Task ConcurrentSameLocation_SecondUpdateBlocked()
    {
        var a = await RegisterHealthyAsync("loc-safe-c1");
        var b = await RegisterHealthyAsync("loc-safe-c2");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.EnqueueAsync(
            b, NodeCommandType.UpdateNodeLatest, "sa2@test", [AdminRole.SuperAdmin]));
    }

    [Fact]
    public async Task OtherLocationHealthy_DoesNotCountAsSibling()
    {
        var a = await RegisterHealthyAsync("loc-safe-d1");
        // Second node in DIFFERENT location.
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 2,
            CreatedBy = "test"
        });
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = "loc-safe-d2-other",
            LocationId = _fx.LocationIdB,
            DisplayName = "other",
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            ServerVersion = "1.1.10",
            Capacity = 100,
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            Endpoints = [new NodeEndpointDto { Host = "other.example", Port = 443, Priority = 1, Enabled = true }]
        });
        MarkHealthy("loc-safe-d2-other");
        MarkHealthy(a);
        await _fx.Db.SaveChangesAsync();

        await Assert.ThrowsAsync<ConflictException>(() => _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]));
    }

    [Fact]
    public async Task ExistingRepair_RequiresBootstrap_InvalidPoPDoesNotConsume()
    {
        var boot1 = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1), MaxUses = 2, CreatedBy = "test"
        });
        var nodeId = "repair-pop-boot";
        var req = new NodeRegisterRequest
        {
            BootstrapToken = boot1.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            ServerVersion = "1.1.10",
            Capacity = 100,
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            Endpoints = [new NodeEndpointDto { Host = "x.example", Port = 443, Priority = 1, Enabled = true }]
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var boot2 = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1), MaxUses = 1, CreatedBy = "test"
        });

        var bad = new NodeRegisterRequest
        {
            BootstrapToken = boot2.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = req.PublicIdentity,
            PublicKey = req.PublicKey,
            ProtocolVersion = 1,
            ServerVersion = "1.1.10",
            Capacity = 100,
            SpkiPin = req.SpkiPin,
            Endpoints = req.Endpoints,
            NodeToken = "not-a-valid-pop-token"
        };
        await Assert.ThrowsAnyAsync<Exception>(() => _fx.Nodes.RegisterWithBootstrapAsync(bad));

        var tokenRow = await _fx.Db.BootstrapTokens.AsNoTracking()
            .SingleAsync(t => t.BootstrapId == boot2.BootstrapId);
        Assert.Equal(0, tokenRow.UsedCount);
    }

    private async Task<string> RegisterHealthyAsync(string nodeId)
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 2,
            CreatedBy = "test"
        });
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            ServerVersion = "1.1.10",
            Capacity = 100,
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1, Enabled = true }]
        });
        MarkHealthy(nodeId);
        await _fx.Db.SaveChangesAsync();
        return nodeId;
    }

    private void MarkHealthy(string nodeId)
    {
        var node = _fx.Db.Nodes.Single(n => n.NodeId == nodeId);
        node.Status = NodeRuntimeStatus.Healthy;
        node.Enabled = true;
        node.Draining = false;
        node.LastSeenAt = _fx.Clock.UtcNow;
        node.LifecycleState = NodeLifecycleState.Active;
    }
}
