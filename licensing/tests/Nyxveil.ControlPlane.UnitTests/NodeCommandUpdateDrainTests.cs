using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeCommandUpdateDrainTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly NodeCommandService _commands;

    public NodeCommandUpdateDrainTests()
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
    public async Task UpdateMarkStarted_SnapshotsAndSetsDraining_ThenRestoreKeepsManualDrain()
    {
        var a = await RegisterHealthyAsync("drain-a");
        var b = await RegisterHealthyAsync("drain-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        // Node A was manually drained before the update was requested.
        await _fx.NodeManagement.SetDrainingAsync(a, true, "admin@test");
        var before = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        Assert.True(before.Draining);

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var midCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        Assert.True(midCfg.Draining);
        var stored = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed.Id);
        Assert.True(NodeCommandService.TryReadAdminSnapshot(
            stored.PayloadJson, out var snapEnabled, out var snapDraining, out var snapMaint));
        Assert.True(NodeCommandService.TryReadDrainEntered(stored.PayloadJson));
        Assert.True(snapEnabled);
        Assert.True(snapDraining);
        Assert.False(snapMaint);

        await _commands.CompleteAsync(claimed.Id, a, success: true, "updated_healthy", "ok");

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.True(afterCfg.Draining);
        Assert.True(afterNode.Draining);
        Assert.True(afterCfg.Enabled);
        Assert.False(afterCfg.MaintenanceMode);
    }

    [Fact]
    public async Task UpdateComplete_RestoresPreviousNonDrainingState()
    {
        var a = await RegisterHealthyAsync("restore-a");
        var b = await RegisterHealthyAsync("restore-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var mid = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.True(mid.Draining);

        await _commands.CompleteAsync(claimed.Id, a, success: true, "updated_healthy", "ok");

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.False(afterCfg.Draining);
        Assert.False(afterNode.Draining);
        Assert.True(afterCfg.Enabled);
        Assert.False(afterCfg.MaintenanceMode);
    }

    [Fact]
    public async Task UpdateComplete_RolledBackHealthy_RestoresAdminState()
    {
        var a = await RegisterHealthyAsync("rb-a");
        var b = await RegisterHealthyAsync("rb-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);
        await _commands.CompleteAsync(claimed.Id, a, success: false, "rolled_back_healthy", "rolled back");

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.False(afterCfg.Draining);
        Assert.False(afterNode.Draining);
        Assert.True(NodeCommandService.ShouldRestoreAdminState(false, "rolled_back_healthy"));
    }

    [Fact]
    public async Task UpdateFailure_HealthFailed_StaysDrained()
    {
        var a = await RegisterHealthyAsync("fail-a");
        var b = await RegisterHealthyAsync("fail-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);
        await _commands.CompleteAsync(claimed.Id, a, success: false, "health_failed", "bad");

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.True(afterCfg.Draining);
        Assert.True(afterNode.Draining);
        Assert.False(NodeCommandService.ShouldRestoreAdminState(false, "health_failed"));
    }

    [Fact]
    public async Task ExpireAfterDrain_StaysDrained_ExpiredOutcomeUnknown()
    {
        var a = await RegisterHealthyAsync("exp-drain-a");
        var b = await RegisterHealthyAsync("exp-drain-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var mid = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.True(mid.Draining);

        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();

        await _commands.ExpireStaleAsync();

        var afterCmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed.Id);
        Assert.Equal(NodeCommandStatus.Failed, afterCmd.Status);
        Assert.Equal("expired_outcome_unknown", afterCmd.ResultCode);

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.True(afterCfg.Draining);
        Assert.True(afterNode.Draining);
    }

    [Fact]
    public async Task ExpireBeforeDrain_PendingOnly_LeavesOriginalState()
    {
        var a = await RegisterHealthyAsync("exp-pend-a");
        var b = await RegisterHealthyAsync("exp-pend-b");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        var cmd = await _commands.EnqueueAsync(
            a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        Assert.Equal(NodeCommandStatus.Pending, cmd.Status);

        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == cmd.Id);
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();

        await _commands.ExpireStaleAsync();

        var afterCmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == cmd.Id);
        Assert.Equal(NodeCommandStatus.Expired, afterCmd.Status);
        Assert.Equal("expired", afterCmd.ResultCode);

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var afterNode = await _fx.Db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == a);
        Assert.False(afterCfg.Draining);
        Assert.False(afterNode.Draining);
        Assert.True(afterCfg.Enabled);
    }

    private async Task<Nyxveil.ControlPlane.Domain.Entities.NodeCommand?> ClaimAfterDrainAsync(string nodeId)
    {
        Assert.Null(await _commands.ClaimNextAsync(nodeId));
        _fx.Clock.Advance(TimeSpan.FromSeconds(1));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = nodeId, CurrentSessions = 0 });
        return await _commands.ClaimNextAsync(nodeId);
    }

    [Theory]
    [InlineData("rolled_back_healthy", false)]
    [InlineData("rollback_failed", true)]
    [InlineData("outcome_unknown", true)]
    [InlineData("rolled_back_unhealthy", true)]
    [InlineData("unexpected_success", true)]
    public async Task LegacySuccessFlag_DoesNotMisreportUpdate(string result, bool staysDrained)
    {
        var a = await RegisterHealthyAsync("legacy-a");
        await RegisterHealthyAsync("legacy-b");
        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa", [AdminRole.SuperAdmin]);
        var command = await ClaimAfterDrainAsync(a);
        await _commands.MarkStartedAsync(command!.Id, a);
        await _commands.CompleteAsync(command.Id, a, true, result, "node result");
        await _commands.CompleteAsync(command.Id, a, true, result, "retry");
        Assert.Equal(NodeCommandStatus.Failed, (await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == command.Id)).Status);
        Assert.Equal(staysDrained, (await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a)).Draining);
    }

    [Fact]
    public async Task DrainWait_IsDurableAndDoesNotBlockHttpRequest()
    {
        var a = await RegisterHealthyAsync("wait-a");
        await RegisterHealthyAsync("wait-b");
        var cmd = await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa", [AdminRole.SuperAdmin]);
        Assert.Null(await _commands.ClaimNextAsync(a).WaitAsync(TimeSpan.FromSeconds(5)));
        _fx.Clock.Advance(TimeSpan.FromSeconds(20));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = a, CurrentSessions = 3 });
        Assert.Null(await _commands.ClaimNextAsync(a).WaitAsync(TimeSpan.FromSeconds(5)));
        _fx.Clock.Advance(TimeSpan.FromSeconds(120));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = a, CurrentSessions = 3 });
        var claimed = await _commands.ClaimNextAsync(a);
        Assert.NotNull(claimed);
        Assert.Equal(cmd.TargetVersion, claimed.TargetVersion);
        Assert.Contains("\"drain_timed_out\":true", claimed.PayloadJson);
    }

    [Fact]
    public void PartialSnapshotCannotRestoreDefaults()
        => Assert.False(NodeCommandService.TryReadAdminSnapshot("{\"admin_state_before\":{}}", out _, out _, out _));

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
    }
}
