using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeCommandServiceTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly NodeCommandService _commands;

    public NodeCommandServiceTests()
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
    public async Task OperatorCanEnqueueRenewCertificate()
    {
        var nodeId = (await RegisterAsync("cmd-renew-ok")).NodeId;
        var cmd = await _commands.EnqueueAsync(
            nodeId,
            NodeCommandType.RenewCertificate,
            "op@test",
            [AdminRole.Operator]);

        Assert.Equal(NodeCommandStatus.Pending, cmd.Status);
        Assert.Equal(NodeCommandType.RenewCertificate, cmd.Type);
        Assert.True(await _fx.Db.AuditLog.AnyAsync(a =>
            a.Action == "node.command.enqueue" && a.EntityId == cmd.Id.ToString("N")));
    }

    [Fact]
    public async Task OperatorCannotEnqueueRebootHost()
    {
        var nodeId = (await RegisterAsync("cmd-reboot-deny")).NodeId;
        await Assert.ThrowsAsync<ForbiddenException>(() => _commands.EnqueueAsync(
            nodeId,
            NodeCommandType.RebootHost,
            "op@test",
            [AdminRole.Operator]));
    }

    [Fact]
    public async Task ReadOnlyCannotEnqueue()
    {
        var nodeId = (await RegisterAsync("cmd-ro-deny")).NodeId;
        await Assert.ThrowsAsync<ForbiddenException>(() => _commands.EnqueueAsync(
            nodeId,
            NodeCommandType.RestartNyxveilService,
            "ro@test",
            [AdminRole.ReadOnly]));
    }

    [Fact]
    public async Task SuperAdminCanEnqueueRebootHost()
    {
        var nodeId = (await RegisterAsync("cmd-reboot-ok")).NodeId;
        var cmd = await _commands.EnqueueAsync(
            nodeId,
            NodeCommandType.RebootHost,
            "sa@test",
            [AdminRole.SuperAdmin]);
        Assert.Equal(NodeCommandType.RebootHost, cmd.Type);
    }

    [Fact]
    public async Task ExpiredPendingCommandIsNotClaimed()
    {
        var nodeId = (await RegisterAsync("cmd-expired")).NodeId;
        var cmd = await _commands.EnqueueAsync(
            nodeId,
            NodeCommandType.RestartNyxveilService,
            "op@test",
            [AdminRole.Operator]);

        cmd.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();

        var claimed = await _commands.ClaimNextAsync(nodeId);
        Assert.Null(claimed);
        var refreshed = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == cmd.Id);
        Assert.Equal(NodeCommandStatus.Expired, refreshed.Status);
    }

    [Fact]
    public async Task WrongNodeCannotCompleteCommand()
    {
        var a = (await RegisterAsync("cmd-wrong-a")).NodeId;
        var b = (await RegisterAsync("cmd-wrong-b")).NodeId;
        var cmd = await _commands.EnqueueAsync(a, NodeCommandType.RenewCertificate, "op@test", [AdminRole.Operator]);
        Assert.NotNull(await _commands.ClaimNextAsync(a));

        await Assert.ThrowsAsync<ForbiddenException>(() =>
            _commands.CompleteAsync(cmd.Id, b, true, "ok", "done"));
    }

    [Fact]
    public async Task DuplicateCompleteIsIdempotentForSameOutcome()
    {
        var nodeId = (await RegisterAsync("cmd-idempotent")).NodeId;
        var cmd = await _commands.EnqueueAsync(nodeId, NodeCommandType.RenewCertificate, "op@test", [AdminRole.Operator]);
        await _commands.ClaimNextAsync(nodeId);
        await _commands.MarkStartedAsync(cmd.Id, nodeId);
        await _commands.CompleteAsync(cmd.Id, nodeId, true, "0", "ok");
        await _commands.CompleteAsync(cmd.Id, nodeId, true, "0", "ok again");

        var refreshed = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == cmd.Id);
        Assert.Equal(NodeCommandStatus.Succeeded, refreshed.Status);
    }

    [Fact]
    public async Task DuplicateCompleteWithDifferentOutcomeIsConflict()
    {
        var nodeId = (await RegisterAsync("cmd-conflict")).NodeId;
        var cmd = await _commands.EnqueueAsync(nodeId, NodeCommandType.RenewCertificate, "op@test", [AdminRole.Operator]);
        await _commands.ClaimNextAsync(nodeId);
        await _commands.CompleteAsync(cmd.Id, nodeId, true, "0", "ok");

        await Assert.ThrowsAsync<ConflictException>(() =>
            _commands.CompleteAsync(cmd.Id, nodeId, false, "1", "fail"));
    }

    [Fact]
    public async Task HeartbeatAcceptsNullOptionalManagementFields()
    {
        var nodeId = (await RegisterAsync("cmd-hb-null")).NodeId;
        var initial = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        initial.SupportsNodeCommands = false;
        initial.ManagementCapabilities = null;
        await _fx.Db.SaveChangesAsync();
        var response = await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 1,
            ManagementCapabilities = null,
            BootId = null,
            SupportsCommands = null
        });

        Assert.True(response.Accepted);
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        Assert.Null(node.ManagementCapabilities);
        Assert.Null(node.LastBootId);
        Assert.False(node.SupportsNodeCommands);
    }

    [Fact]
    public async Task HeartbeatAppliesManagementFieldsWhenPresent()
    {
        var nodeId = (await RegisterAsync("cmd-hb-apply")).NodeId;
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            ManagementCapabilities = "certificate_renew,service_restart",
            BootId = "boot-1",
            SupportsCommands = true
        });

        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal("certificate_renew,service_restart", node.ManagementCapabilities);
        Assert.Equal("boot-1", node.LastBootId);
        Assert.True(node.SupportsNodeCommands);
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
        await EnsureHealthySiblingAsync();
        MarkHealthy(nodeId);
        await _fx.Db.SaveChangesAsync();
        return request;
    }

    private async Task EnsureHealthySiblingAsync()
    {
        const string siblingId = "cmd-loc-sibling";
        if (await _fx.Db.Nodes.AnyAsync(n => n.NodeId == siblingId))
        {
            MarkHealthy(siblingId);
            return;
        }

        var bootstrap = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = bootstrap.BootstrapToken,
            NodeId = siblingId,
            LocationId = _fx.LocationId,
            DisplayName = siblingId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            ServerVersion = "1.1.0",
            Capacity = 10,
            Endpoints = [new NodeEndpointDto { Host = "sibling.example", Port = 443, Enabled = true }]
        });
        MarkHealthy(siblingId);
    }

    private void MarkHealthy(string nodeId)
    {
        var node = _fx.Db.Nodes.Single(n => n.NodeId == nodeId);
        node.SupportsNodeCommands = true;
        node.ManagementCapabilities = "certificate_renew,service_restart,host_reboot,node_update";
        node.Status = NodeRuntimeStatus.Healthy;
        node.Enabled = true;
        node.Draining = false;
        node.LastSeenAt = _fx.Clock.UtcNow;
        node.LifecycleState = NodeLifecycleState.Active;
    }
}
