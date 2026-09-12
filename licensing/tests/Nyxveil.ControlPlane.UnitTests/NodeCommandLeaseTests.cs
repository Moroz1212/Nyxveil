using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// LIVE 1.1.15→1.1.16 expired at ~15m while drain/update was in flight because ClaimNext
/// returned null during drain wait without refreshing ExpiresAt past DeliveryTtl.
/// </summary>
public sealed class NodeCommandLeaseTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly NodeCommandService _commands;

    public NodeCommandLeaseTests()
    {
        _commands = new NodeCommandService(
            _fx.Db,
            _fx.Clock,
            _fx.Scope.ServiceProvider.GetRequiredService<IAuditService>(),
            new FakeServerReleaseService
            {
                Info = new ServerReleaseInfo
                {
                    LatestVersion = "1.1.17",
                    ReleaseTag = "server-v1.1.17",
                    SourceStatus = "ok",
                    LastCheckedAt = DateTimeOffset.UtcNow
                }
            });
    }

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task DrainWait_RefreshesLease_PastDeliveryTtl()
    {
        var a = await RegisterHealthyAsync("lease-a");
        await RegisterHealthyAsync("lease-b");

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var deliveryExpiry = cmd.ExpiresAt;

        // First claim enters drain and returns null (waiting for post-drain heartbeat).
        Assert.Null(await _commands.ClaimNextAsync(a));
        var afterDrain = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == cmd.Id);
        Assert.True(NodeCommandService.TryReadDrainEntered(afterDrain.PayloadJson));
        Assert.True(NodeCommandService.TryReadExecutionDeadline(afterDrain.PayloadJson, out var absolute));
        Assert.True(afterDrain.ExpiresAt > deliveryExpiry);
        Assert.True(absolute > _fx.Clock.UtcNow.AddMinutes(60));

        // Simulate LIVE: ~16 minutes of drain-wait polls with ClaimNext returning null.
        // Heartbeat only the sibling (location safety). Keep target LastSeenAt older than
        // ProgressUpdatedAt so ClaimNext stays in the drain-wait path (the LIVE failure window).
        for (var i = 0; i < 20; i++)
        {
            _fx.Clock.Advance(TimeSpan.FromMinutes(1));
            await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = "lease-b", CurrentSessions = 0 });
            Assert.Null(await _commands.ClaimNextAsync(a));
            var row = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == cmd.Id);
            Assert.Equal(NodeCommandStatus.Pending, row.Status);
            Assert.True(row.ExpiresAt > _fx.Clock.UtcNow,
                $"lease must remain live at t+{i + 1}m; expires={row.ExpiresAt:o} now={_fx.Clock.UtcNow:o}");
        }

        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = a, CurrentSessions = 0 });
        var claimed = await _commands.ClaimNextAsync(a);
        Assert.NotNull(claimed);
        Assert.Equal(NodeCommandStatus.Claimed, claimed!.Status);
    }

    [Fact]
    public async Task LateResult_ReconcilesGenericExpired()
    {
        var a = await RegisterHealthyAsync("late-a");
        await RegisterHealthyAsync("late-b");

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        Assert.Null(await _commands.ClaimNextAsync(a));
        _fx.Clock.Advance(TimeSpan.FromSeconds(1));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = a, CurrentSessions = 0 });
        var claimed = await _commands.ClaimNextAsync(a);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.Status = NodeCommandStatus.Expired;
        row.ResultCode = "expired";
        row.ResultMessage = "command TTL exceeded";
        row.CompletedAt = _fx.Clock.UtcNow;
        await _fx.Db.SaveChangesAsync();

        await _commands.CompleteAsync(claimed.Id, a, success: true, "updated_healthy", "node finished after CP TTL");

        var after = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed.Id);
        Assert.Equal(NodeCommandStatus.Succeeded, after.Status);
        Assert.Equal("updated_healthy", after.ResultCode);
        Assert.Contains("late_result_reconciled", after.ResultMessage);
    }

    [Fact]
    public async Task ProgressReport_ExtendsLease()
    {
        var a = await RegisterHealthyAsync("prog-a");
        await RegisterHealthyAsync("prog-b");
        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa", [AdminRole.SuperAdmin]);
        Assert.Null(await _commands.ClaimNextAsync(a));
        _fx.Clock.Advance(TimeSpan.FromSeconds(1));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = a, CurrentSessions = 0 });
        var claimed = await _commands.ClaimNextAsync(a);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var before = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed!.Id);
        _fx.Clock.Advance(TimeSpan.FromMinutes(10));
        await _commands.ReportProgressAsync(claimed!.Id, a, "installing", "copying binaries");
        var after = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed.Id);
        Assert.Equal("installing", after.ProgressPhase);
        Assert.True(after.ExpiresAt > before.ExpiresAt);
        Assert.True(after.ExpiresAt > _fx.Clock.UtcNow);
    }

    [Fact]
    public async Task AbsoluteExecutionTimeout_StillExpires()
    {
        var a = await RegisterHealthyAsync("abs-a");
        await RegisterHealthyAsync("abs-b");
        var cmd = await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa", [AdminRole.SuperAdmin]);
        Assert.Null(await _commands.ClaimNextAsync(a));

        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == cmd.Id);
        // Force absolute deadline into the past while keeping ExpiresAt stale.
        var payload = System.Text.Json.Nodes.JsonNode.Parse(row.PayloadJson!)!.AsObject();
        payload["execution_deadline"] = _fx.Clock.UtcNow.AddMinutes(-1).ToString("O");
        row.PayloadJson = payload.ToJsonString();
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        row.ProgressUpdatedAt = _fx.Clock.UtcNow.AddMinutes(-30);
        await _fx.Db.SaveChangesAsync();

        await _commands.ExpireStaleAsync();
        var after = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == cmd.Id);
        Assert.Equal(NodeCommandStatus.Failed, after.Status);
        Assert.Equal("expired_outcome_unknown", after.ResultCode);
        Assert.Contains("execution timeout", after.ResultMessage!, StringComparison.OrdinalIgnoreCase);
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
            ServerVersion = "1.1.15",
            Capacity = 100,
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1, Enabled = true }]
        });
        var node = _fx.Db.Nodes.Single(n => n.NodeId == nodeId);
        node.SupportsNodeCommands = true;
        node.ManagementCapabilities = "certificate_renew,service_restart,host_reboot,node_update";
        node.Status = NodeRuntimeStatus.Healthy;
        node.Enabled = true;
        node.Draining = false;
        node.CurrentSessions = 0;
        node.LastSeenAt = _fx.Clock.UtcNow;
        node.LifecycleState = NodeLifecycleState.Active;
        var cfg = _fx.Db.NodeConfigs.Single(c => c.NodeId == nodeId);
        cfg.Enabled = true;
        cfg.Draining = false;
        cfg.MaintenanceMode = false;
        await _fx.Db.SaveChangesAsync();
        return nodeId;
    }
}
