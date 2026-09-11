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

public sealed class UnknownUpdateReconciliationTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly NodeCommandService _commands;

    public UnknownUpdateReconciliationTests()
    {
        _commands = new NodeCommandService(
            _fx.Db,
            _fx.Clock,
            _fx.Scope.ServiceProvider.GetRequiredService<IAuditService>(),
            new FakeServerReleaseService
            {
                Info = new ServerReleaseInfo
                {
                    LatestVersion = "1.1.12",
                    ReleaseTag = "server-v1.1.12",
                    SourceStatus = "ok",
                    LastCheckedAt = DateTimeOffset.UtcNow
                }
            });
    }

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task LiveScenario_ConfirmRollback_RestoresAdminAndReleasesLocationLock()
    {
        var a = await RegisterHealthyAsync("rec-live-a", "1.1.9");
        var b = await RegisterHealthyAsync("rec-live-b", "1.1.9");
        MarkHealthy(a);
        MarkHealthy(b);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        Assert.NotNull(claimed);
        await _commands.MarkStartedAsync(claimed!.Id, a);

        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();
        await _commands.ExpireStaleAsync();

        var stuck = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.Id == claimed.Id);
        Assert.Equal("expired_outcome_unknown", stuck.ResultCode);
        Assert.True((await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a)).Draining);

        await Assert.ThrowsAsync<ConflictException>(() =>
            _commands.EnqueueAsync(b, NodeCommandType.UpdateNodeLatest, "sa2@test", [AdminRole.SuperAdmin]));

        SetObservedVersion(a, "1.1.9");
        await _fx.Db.SaveChangesAsync();

        var result = await _commands.ReconcileUnknownUpdateAsync(new UnknownUpdateReconciliationRequest
        {
            CommandId = claimed.Id,
            NodeId = a,
            Action = UnknownUpdateReconciliationAction.ConfirmRollback,
            Actor = "sa@test",
            Roles = [AdminRole.SuperAdmin],
            Reason = "LIVE: 1.1.9→1.1.12 expired; node still on 1.1.9 after rollback"
        });

        Assert.False(result.IdempotentReplay);
        Assert.Equal(NodeCommandStatus.Failed, result.Command.Status);
        Assert.Equal("rolled_back_healthy", result.Command.ResultCode);
        Assert.Contains("\"original_result_code\":\"expired_outcome_unknown\"", result.Command.PayloadJson);
        Assert.Contains("\"action\":\"confirm_rollback\"", result.Command.PayloadJson);

        var afterCfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        Assert.False(afterCfg.Draining);
        Assert.True(afterCfg.Enabled);
        Assert.False(afterCfg.MaintenanceMode);

        // Location lock released — sibling update may proceed.
        var next = await _commands.EnqueueAsync(
            b, NodeCommandType.UpdateNodeLatest, "sa2@test", [AdminRole.SuperAdmin]);
        Assert.Equal(NodeCommandStatus.Pending, next.Status);

        var audits = await _fx.Db.AuditLog.AsNoTracking()
            .Where(x => x.Action == "node.command.update.reconcile")
            .ToListAsync();
        Assert.Contains(audits, x => x.Actor == "sa@test" && x.EntityId == claimed.Id.ToString("N"));
    }

    [Fact]
    public async Task ConfirmUpdated_WhenObservedEqualsTarget()
    {
        var a = await SeedUnknownAsync("rec-upd-a", previous: "1.1.9", target: "1.1.13", observed: "1.1.13");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);

        var result = await _commands.ReconcileUnknownUpdateAsync(new UnknownUpdateReconciliationRequest
        {
            CommandId = cmd.Id,
            NodeId = a,
            Action = UnknownUpdateReconciliationAction.ConfirmUpdated,
            Actor = "sa@test",
            Roles = [AdminRole.SuperAdmin],
            Reason = "node reports target version"
        });

        Assert.Equal(NodeCommandStatus.Succeeded, result.Command.Status);
        Assert.Equal("updated_healthy", result.Command.ResultCode);
        Assert.False((await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a)).Draining);
    }

    [Fact]
    public async Task VersionMismatch_FailsClosed()
    {
        var a = await SeedUnknownAsync("rec-mm-a", "1.1.9", "1.1.12", "1.1.10");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            new UnknownUpdateReconciliationRequest
            {
                CommandId = cmd.Id,
                NodeId = a,
                Action = UnknownUpdateReconciliationAction.ConfirmRollback,
                Actor = "sa@test",
                Roles = [AdminRole.SuperAdmin],
                Reason = "attempt"
            }));
    }

    [Fact]
    public async Task StaleHeartbeat_Fails()
    {
        var a = await SeedUnknownAsync("rec-stale-a", "1.1.9", "1.1.12", "1.1.9");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == a);
        node.LastSeenAt = _fx.Clock.UtcNow.AddMinutes(-10);
        await _fx.Db.SaveChangesAsync();
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task DeletedNode_Fails()
    {
        var a = await SeedUnknownAsync("rec-del-a", "1.1.9", "1.1.12", "1.1.9");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == a);
        node.LifecycleState = NodeLifecycleState.Deleted;
        await _fx.Db.SaveChangesAsync();
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task RevokedNode_Fails()
    {
        var a = await SeedUnknownAsync("rec-rev-a", "1.1.9", "1.1.12", "1.1.9");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == a);
        node.LifecycleState = NodeLifecycleState.Revoked;
        await _fx.Db.SaveChangesAsync();
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task ActiveCommand_Fails()
    {
        var a = await RegisterHealthyAsync("rec-act-a", "1.1.9");
        await RegisterHealthyAsync("rec-act-b", "1.1.9");
        MarkHealthy(a);
        MarkHealthy("rec-act-b");
        await _fx.Db.SaveChangesAsync();
        var cmd = await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task OrdinaryFailed_Fails()
    {
        var a = await SeedUnknownAsync("rec-ord-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.ResultCode = "health_failed";
        await _fx.Db.SaveChangesAsync();
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task EmptyReason_Fails()
    {
        var a = await SeedUnknownAsync("rec-reason-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var req = Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback);
        req.Reason = "  ";
        await Assert.ThrowsAsync<ValidationException>(() => _commands.ReconcileUnknownUpdateAsync(req));
    }

    [Fact]
    public async Task NonSuperAdmin_Fails()
    {
        var a = await SeedUnknownAsync("rec-role-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var req = Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback);
        req.Roles = [AdminRole.Operator];
        await Assert.ThrowsAsync<ForbiddenException>(() => _commands.ReconcileUnknownUpdateAsync(req));
    }

    [Fact]
    public async Task IdempotentReplay_SameAction()
    {
        var a = await SeedUnknownAsync("rec-idem-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var first = await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        var second = await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        Assert.False(first.IdempotentReplay);
        Assert.True(second.IdempotentReplay);
        Assert.Equal(1, await _fx.Db.AuditLog.CountAsync(x => x.Action == "node.command.update.reconcile"
            && x.EntityId == cmd.Id.ToString("N")));
    }

    [Fact]
    public async Task RollbackFailed_WithPreviousVersion_Allowed()
    {
        var a = await SeedUnknownAsync("rec-rbf-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.ResultCode = "rollback_failed";
        cmd.ResultMessage = "node reported rollback_failed";
        await _fx.Db.SaveChangesAsync();

        var result = await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        Assert.Equal("rolled_back_healthy", result.Command.ResultCode);
    }

    [Fact]
    public async Task RollbackFailed_AmbiguousVersion_Fails()
    {
        var a = await SeedUnknownAsync("rec-rbf2-a", "1.1.9", "1.1.12", "1.1.10");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.ResultCode = "rollback_failed";
        await _fx.Db.SaveChangesAsync();
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task PriorManualDrain_IsPreserved()
    {
        var a = await RegisterHealthyAsync("rec-md-a", "1.1.9");
        await RegisterHealthyAsync("rec-md-b", "1.1.9");
        MarkHealthy(a);
        MarkHealthy("rec-md-b");
        await _fx.Db.SaveChangesAsync();
        await _fx.NodeManagement.SetDrainingAsync(a, true, "admin@test");

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        await _commands.MarkStartedAsync(claimed!.Id, a);
        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();
        await _commands.ExpireStaleAsync();
        SetObservedVersion(a, "1.1.9");
        await _fx.Db.SaveChangesAsync();

        await _commands.ReconcileUnknownUpdateAsync(
            Req(claimed.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));

        Assert.True((await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a)).Draining);
    }

    [Fact]
    public async Task MissingAdminSnapshot_Fails()
    {
        var a = await SeedUnknownAsync("rec-snap-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.PayloadJson = "{\"drain_entered\":true}";
        await _fx.Db.SaveChangesAsync();
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task AlreadyReconciled_DifferentAction_Conflicts()
    {
        var a = await SeedUnknownAsync("rec-race-a", "1.1.9", "1.1.13", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        // Observed still previous — ConfirmUpdated is not allowed by evidence, but also already reconciled.
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmUpdated)));
    }

    [Fact]
    public async Task UnsupportedCommandType_Fails()
    {
        var a = await SeedUnknownAsync("rec-type-a", "1.1.9", "1.1.12", "1.1.9");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.Type = NodeCommandType.RestartNyxveilService;
        await _fx.Db.SaveChangesAsync();
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task OrdinarySucceeded_Fails()
    {
        var a = await SeedUnknownAsync("rec-ok-a", "1.1.9", "1.1.12", "1.1.12");
        var cmd = await _fx.Db.NodeCommands.SingleAsync(c => c.NodeId == a);
        cmd.Status = NodeCommandStatus.Succeeded;
        cmd.ResultCode = "updated_healthy";
        await _fx.Db.SaveChangesAsync();
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmUpdated)));
    }

    [Fact]
    public async Task UnhealthyRuntime_Fails()
    {
        var a = await SeedUnknownAsync("rec-uh-a", "1.1.9", "1.1.12", "1.1.9");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == a);
        node.Status = NodeRuntimeStatus.Offline;
        await _fx.Db.SaveChangesAsync();
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task ActiveSessions_Fails()
    {
        var a = await SeedUnknownAsync("rec-sess-a", "1.1.9", "1.1.12", "1.1.9");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == a);
        node.CurrentSessions = 2;
        await _fx.Db.SaveChangesAsync();
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await Assert.ThrowsAsync<ConflictException>(() => _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback)));
    }

    [Fact]
    public async Task PriorMaintenance_IsPreserved()
    {
        var a = await RegisterHealthyAsync("rec-mnt-a", "1.1.9");
        await RegisterHealthyAsync("rec-mnt-b", "1.1.9");
        MarkHealthy(a);
        MarkHealthy("rec-mnt-b");
        await _fx.Db.SaveChangesAsync();
        await _fx.NodeManagement.EnterMaintenanceAsync(a, "admin@test");

        await _commands.EnqueueAsync(a, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(a);
        await _commands.MarkStartedAsync(claimed!.Id, a);
        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();
        await _commands.ExpireStaleAsync();
        SetObservedVersion(a, "1.1.9");
        await _fx.Db.SaveChangesAsync();

        await _commands.ReconcileUnknownUpdateAsync(
            Req(claimed.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));

        var cfg = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        Assert.True(cfg.MaintenanceMode);
        Assert.False(cfg.Draining);
    }

    [Fact]
    public async Task SiblingNode_NotMutated()
    {
        var a = await SeedUnknownAsync("rec-sib-a", "1.1.9", "1.1.12", "1.1.9");
        var sibling = "rec-sib-a-sib";
        var before = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == sibling);
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        var after = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == sibling);
        Assert.Equal(before.ConfigVersion, after.ConfigVersion);
        Assert.Equal(before.Draining, after.Draining);
        Assert.Equal(before.Enabled, after.Enabled);
        Assert.Equal(before.MaintenanceMode, after.MaintenanceMode);
    }

    [Fact]
    public async Task ConfirmRollback_IncrementsConfigVersion()
    {
        var a = await SeedUnknownAsync("rec-cfg-a", "1.1.9", "1.1.12", "1.1.9");
        var before = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        var cmd = await _fx.Db.NodeCommands.AsNoTracking().SingleAsync(c => c.NodeId == a);
        await _commands.ReconcileUnknownUpdateAsync(
            Req(cmd.Id, a, UnknownUpdateReconciliationAction.ConfirmRollback));
        var after = await _fx.Db.NodeConfigs.AsNoTracking().SingleAsync(c => c.NodeId == a);
        Assert.Equal(before.ConfigVersion + 1, after.ConfigVersion);
    }

    private static UnknownUpdateReconciliationRequest Req(
        Guid id, string nodeId, UnknownUpdateReconciliationAction action) => new()
    {
        CommandId = id,
        NodeId = nodeId,
        Action = action,
        Actor = "sa@test",
        Roles = [AdminRole.SuperAdmin],
        Reason = "unit test reconciliation"
    };

    private async Task<string> SeedUnknownAsync(
        string nodeId, string previous, string target, string observed)
    {
        var sibling = nodeId + "-sib";
        await RegisterHealthyAsync(nodeId, previous);
        await RegisterHealthyAsync(sibling, previous);
        MarkHealthy(nodeId);
        MarkHealthy(sibling);
        await _fx.Db.SaveChangesAsync();

        await _commands.EnqueueAsync(nodeId, NodeCommandType.UpdateNodeLatest, "sa@test", [AdminRole.SuperAdmin]);
        var claimed = await ClaimAfterDrainAsync(nodeId);
        await _commands.MarkStartedAsync(claimed!.Id, nodeId);
        var row = await _fx.Db.NodeCommands.SingleAsync(c => c.Id == claimed.Id);
        row.PreviousVersion = previous;
        row.TargetVersion = target;
        row.ExpiresAt = _fx.Clock.UtcNow.AddMinutes(-1);
        await _fx.Db.SaveChangesAsync();
        await _commands.ExpireStaleAsync();
        SetObservedVersion(nodeId, observed);
        await _fx.Db.SaveChangesAsync();
        return nodeId;
    }

    private void SetObservedVersion(string nodeId, string version)
    {
        var node = _fx.Db.Nodes.Single(n => n.NodeId == nodeId);
        node.ReportedServerVersion = version;
        node.ServerVersion = version;
        node.VersionReportedAt = _fx.Clock.UtcNow;
        node.LastSeenAt = _fx.Clock.UtcNow;
        node.Status = NodeRuntimeStatus.Healthy;
        node.CurrentSessions = 0;
    }

    private async Task<Nyxveil.ControlPlane.Domain.Entities.NodeCommand?> ClaimAfterDrainAsync(string nodeId)
    {
        Assert.Null(await _commands.ClaimNextAsync(nodeId));
        _fx.Clock.Advance(TimeSpan.FromSeconds(1));
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = nodeId, CurrentSessions = 0 });
        return await _commands.ClaimNextAsync(nodeId);
    }

    private async Task<string> RegisterHealthyAsync(string nodeId, string serverVersion)
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
            ServerVersion = serverVersion,
            Capacity = 100,
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1, Enabled = true }]
        });
        MarkHealthy(nodeId);
        SetObservedVersion(nodeId, serverVersion);
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
