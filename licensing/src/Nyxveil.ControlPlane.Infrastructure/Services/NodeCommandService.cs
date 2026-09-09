using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class NodeCommandService : INodeCommandService
{
    public static readonly TimeSpan PendingTtl = TimeSpan.FromMinutes(15);
    public static readonly TimeSpan RunningTtl = TimeSpan.FromMinutes(30);
    public static readonly TimeSpan RebootRunningTtl = TimeSpan.FromMinutes(45);
    public static readonly TimeSpan UpdateRunningTtl = TimeSpan.FromMinutes(60);
    public static readonly TimeSpan HeartbeatFreshness = TimeSpan.FromMinutes(5);
    /// <summary>
    /// Bounded wait for CurrentSessions==0 after setting Draining=true before update proceeds.
    /// If sessions remain after this window, update continues (sessions may be disrupted) and
    /// drain_timed_out is recorded on the command payload — existing soft disruption policy.
    /// </summary>
    public static readonly TimeSpan UpdateDrainWait = TimeSpan.FromSeconds(120);
    public static readonly TimeSpan UpdateDrainPoll = TimeSpan.FromSeconds(2);

    /// <summary>
    /// Result codes that indicate a safe no-mutation / healthy rollback path where admin
    /// state (including prior drain) may be restored after UpdateNodeLatest.
    /// </summary>
    private static readonly HashSet<string> SafeRestoreResultCodes = new(StringComparer.OrdinalIgnoreCase)
    {
        "no_mutation_failed",
        "rolled_back_healthy",
        "already_current",
        "ahead",
        "target_missing"
    };

    private static readonly NodeCommandType[] DisruptiveTypes =
    [
        NodeCommandType.UpdateNodeLatest,
        NodeCommandType.RestartNyxveilService,
        NodeCommandType.RebootHost
    ];

    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;
    private readonly IAuditService _audit;
    private readonly IServerReleaseService _releases;

    public NodeCommandService(
        ControlPlaneDbContext db,
        IClock clock,
        IAuditService audit,
        IServerReleaseService releases)
    {
        _db = db;
        _clock = clock;
        _audit = audit;
        _releases = releases;
    }

    public async Task<NodeCommand> EnqueueAsync(
        string nodeId,
        NodeCommandType type,
        string actor,
        IEnumerable<string> roles,
        CancellationToken cancellationToken = default)
    {
        return await WithLocationLockAsync(nodeId,
            () => EnqueueLockedAsync(nodeId, type, actor, roles, cancellationToken), cancellationToken);
    }

    private async Task<NodeCommand> EnqueueLockedAsync(string nodeId, NodeCommandType type,
        string actor, IEnumerable<string> roles, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");
        if (string.IsNullOrWhiteSpace(actor))
            throw new ValidationException("actor is required");

        AssertCanEnqueue(type, roles);
        await ExpireStaleForNodeAsync(nodeId, cancellationToken);

        if (!Enum.IsDefined(type))
            throw new ValidationException("unsupported command type");

        if (await _db.NodeCommands.AsNoTracking().AnyAsync(c => c.NodeId == nodeId
            && (c.Status == NodeCommandStatus.Pending || c.Status == NodeCommandStatus.Claimed
                || c.Status == NodeCommandStatus.Running || c.Status == NodeCommandStatus.Executing
                || c.Status == NodeCommandStatus.Accepted), cancellationToken))
            throw new ConflictException("another command is already active on this node");

        if (IsDisruptive(type))
        {
            // Resolve location under AsNoTracking, then hold the per-location gate for the
            // entire assert+insert path (process-level; complements SQL Serializable).
            var locId = await _db.Nodes.AsNoTracking()
                .Where(n => n.NodeId == nodeId)
                .Select(n => n.LocationId)
                .FirstOrDefaultAsync(cancellationToken)
                .ConfigureAwait(false)
                ?? throw new NotFoundException("node not found");

                var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
                    .ConfigureAwait(false)
                    ?? throw new NotFoundException("node not found");
                if (node.LifecycleState is NodeLifecycleState.Deleted or NodeLifecycleState.Revoked)
                    throw new ForbiddenException("node deleted/revoked");
                return await EnqueueDisruptiveCoreAsync(node, type, actor, cancellationToken)
                    .ConfigureAwait(false);
        }

        var nodeNonDisruptive = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node not found");
        if (nodeNonDisruptive.LifecycleState is NodeLifecycleState.Deleted or NodeLifecycleState.Revoked)
            throw new ForbiddenException("node deleted/revoked");

        return await EnqueueCoreAsync(nodeNonDisruptive, type, actor, cancellationToken)
            .ConfigureAwait(false);
    }

    private async Task<NodeCommand> EnqueueDisruptiveCoreAsync(
        Node node,
        NodeCommandType type,
        string actor,
        CancellationToken cancellationToken)
    {
            // Re-load node under the transaction for a consistent location id / lifecycle snapshot.
            node = await _db.Nodes.FirstAsync(n => n.NodeId == node.NodeId, cancellationToken)
                .ConfigureAwait(false);
            if (node.LifecycleState is NodeLifecycleState.Deleted or NodeLifecycleState.Revoked)
                throw new ForbiddenException("node deleted/revoked");

            await AssertLocationDisruptionAllowedAsync(node, excludeCommandId: null, cancellationToken)
                .ConfigureAwait(false);

            var command = await EnqueueCoreAsync(node, type, actor, cancellationToken)
                .ConfigureAwait(false);
            return command;
    }

    private async Task<NodeCommand> EnqueueCoreAsync(
        Node node,
        NodeCommandType type,
        string actor,
        CancellationToken cancellationToken)
    {
        var now = _clock.UtcNow;
        if (!NodeCommandCapabilities.Supports(node, type))
            throw new ConflictException("node has not advertised support for this command");
        var command = new NodeCommand
        {
            Id = Guid.NewGuid(),
            NodeId = node.NodeId,
            Type = type,
            Status = NodeCommandStatus.Pending,
            CreatedAt = now,
            CreatedBy = actor.Trim(),
            IssuedAt = now,
            ExpiresAt = now.Add(PendingTtl),
            AttemptCount = 0,
            CorrelationId = Guid.NewGuid(),
            PreviousVersion = NodeVersionEvaluator.EffectiveInstalledVersion(
                node.ReportedServerVersion, node.ServerVersion)
        };

        if (type == NodeCommandType.UpdateNodeLatest)
        {
            var latest = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
            if (latest.SourceStatus is not ("ok" or "cached")
                || !SemVersion.TryParse(latest.LatestVersion, out var target) || target.IsPrerelease
                || latest.ReleaseTag != "server-v" + target)
                throw new ConflictException("latest stable server release is unknown; refresh Server Releases and retry");
            command.TargetVersion = target.ToString();
            command.PayloadJson =
                $"{{\"target_version\":\"{Escape(command.TargetVersion)}\",\"release_tag\":\"{Escape(latest.ReleaseTag)}\"}}";
        }

        _db.NodeCommands.Add(command);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = actor,
            Action = "node.command.enqueue",
            EntityType = "NodeCommand",
            EntityId = command.Id.ToString("N"),
            Detail =
                $"{{\"node_id\":\"{node.NodeId}\",\"type\":\"{type}\",\"target_version\":\"{Escape(command.TargetVersion)}\"}}"
        }, cancellationToken).ConfigureAwait(false);

        return command;
    }

    public async Task<IReadOnlyList<NodeCommand>> ListForNodeAsync(
        string nodeId,
        int take = 50,
        CancellationToken cancellationToken = default)
    {
        take = Math.Clamp(take, 1, 500);
        return await _db.NodeCommands.AsNoTracking()
            .Where(c => c.NodeId == nodeId)
            .OrderByDescending(c => c.CreatedAt)
            .Take(take)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);
    }

    public async Task<IReadOnlyList<NodeCommand>> ListRecentAsync(
        int take = 100,
        CancellationToken cancellationToken = default)
    {
        take = Math.Clamp(take, 1, 500);
        return await _db.NodeCommands.AsNoTracking()
            .OrderByDescending(c => c.CreatedAt)
            .Take(take)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);
    }

    public async Task<NodeCommand?> ClaimNextAsync(string nodeId, CancellationToken cancellationToken = default)
        => await WithLocationLockAsync(nodeId, () => ClaimNextLockedAsync(nodeId, cancellationToken), cancellationToken);

    private async Task<NodeCommand?> ClaimNextLockedAsync(string nodeId, CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        var now = _clock.UtcNow;
        await ExpireStaleForNodeAsync(nodeId, cancellationToken).ConfigureAwait(false);

        while (true)
        {
            var command = await _db.NodeCommands
                .Where(c => c.NodeId == nodeId
                            && c.Status == NodeCommandStatus.Pending
                            && c.ExpiresAt > now)
                .OrderBy(c => c.IssuedAt)
                .FirstOrDefaultAsync(cancellationToken)
                .ConfigureAwait(false);

            if (command is null)
                return null;

            if (IsDisruptive(command.Type))
            {
                var node = await _db.Nodes.FirstAsync(n => n.NodeId == nodeId, cancellationToken)
                    .ConfigureAwait(false);
                try
                {
                    await AssertLocationDisruptionAllowedAsync(node, command.Id, cancellationToken)
                        .ConfigureAwait(false);
                }
                catch (ConflictException ex)
                {
                    command.Status = NodeCommandStatus.Failed;
                    command.CompletedAt = now;
                    command.ResultCode = "location_safety";
                    command.ResultMessage = Truncate(ex.Message, 1024);
                    await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
                    continue;
                }
            }

            if (command.Type == NodeCommandType.UpdateNodeLatest)
            {
                if (!TryReadDrainEntered(command.PayloadJson))
                    await PrepareUpdateDrainAsync(command, nodeId, cancellationToken);
                var drainingNode = await _db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId, cancellationToken);
                // Durable wait across short polls: Node's HTTP timeout is 30 seconds.
                // Require a heartbeat received after drain was published before dispatch.
                if (drainingNode.LastSeenAt <= command.ProgressUpdatedAt || drainingNode.LastSeenAt is null)
                    return null;
                if (drainingNode.CurrentSessions > 0 && _clock.UtcNow < command.ProgressUpdatedAt + UpdateDrainWait)
                    return null;
                if (drainingNode.CurrentSessions > 0)
                {
                    var payload = JsonNode.Parse(command.PayloadJson!)!.AsObject();
                    payload["drain_timed_out"] = true;
                    command.PayloadJson = payload.ToJsonString();
                }
            }

            command.Status = NodeCommandStatus.Claimed;
            command.ClaimedAt = now;
            command.AttemptCount += 1;
            var runningTtl = command.Type switch
            {
                NodeCommandType.RebootHost => RebootRunningTtl,
                NodeCommandType.UpdateNodeLatest => UpdateRunningTtl,
                _ => RunningTtl
            };
            command.ExpiresAt = now.Add(runningTtl);

            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            return command;
        }
    }

    public async Task MarkStartedAsync(Guid id, string nodeId, CancellationToken cancellationToken = default)
        => await WithLocationLockAsync(nodeId, async () => { await MarkStartedLockedAsync(id, nodeId, cancellationToken); return true; }, cancellationToken);

    private async Task MarkStartedLockedAsync(Guid id, string nodeId, CancellationToken cancellationToken)
    {
        var command = await GetOwnedCommandAsync(id, nodeId, cancellationToken).ConfigureAwait(false);
        if (command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.Failed
            or NodeCommandStatus.Expired or NodeCommandStatus.Cancelled or NodeCommandStatus.NodeReturned)
            throw new ConflictException("command already completed");

        if (command.Status is not (NodeCommandStatus.Claimed or NodeCommandStatus.Running
            or NodeCommandStatus.Executing or NodeCommandStatus.Accepted))
            throw new ValidationException($"command cannot start from status {command.Status}");

        var now = _clock.UtcNow;
        if (command.Status == NodeCommandStatus.Claimed)
        {
            var commandNode = await _db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId, cancellationToken);
            if (commandNode.LifecycleState != NodeLifecycleState.Active || !NodeCommandCapabilities.Supports(commandNode, command.Type))
                throw new ConflictException("node has not advertised support for this command");

            if (IsDisruptive(command.Type))
            {
                var targetNode = await _db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == nodeId, cancellationToken);
                await AssertLocationDisruptionAllowedAsync(targetNode, command.Id, cancellationToken);
            }
            if (command.Type == NodeCommandType.UpdateNodeLatest && !TryReadDrainEntered(command.PayloadJson))
                await PrepareUpdateDrainAsync(command, nodeId, cancellationToken).ConfigureAwait(false);

            command.Status = NodeCommandStatus.Running;
            command.StartedAt = now;
            var runningTtl = command.Type switch
            {
                NodeCommandType.RebootHost => RebootRunningTtl,
                NodeCommandType.UpdateNodeLatest => UpdateRunningTtl,
                _ => RunningTtl
            };
            command.ExpiresAt = now.Add(runningTtl);
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        }
    }

    public async Task CompleteAsync(
        Guid id,
        string nodeId,
        bool success,
        string? resultCode,
        string? resultMessage,
        string? bootId = null,
        CancellationToken cancellationToken = default)
    {
        await WithLocationLockAsync(nodeId, async () =>
        {
            await CompleteLockedAsync(id, nodeId, success, resultCode, resultMessage, bootId, cancellationToken);
            return true;
        }, cancellationToken);
    }

    private async Task CompleteLockedAsync(Guid id, string nodeId, bool success, string? resultCode,
        string? resultMessage, string? bootId, CancellationToken cancellationToken)
    {
        var command = await GetOwnedCommandAsync(id, nodeId, cancellationToken).ConfigureAwait(false);
        resultCode = resultCode?.Trim();
        // Older nodes incorrectly report healthy rollback as success. Normalize before idempotency.
        if (command.Type == NodeCommandType.UpdateNodeLatest)
            success = success && resultCode is "updated_healthy" or "already_current" or "ahead";
        if (command.Status == NodeCommandStatus.Pending)
            throw new ConflictException("unclaimed command cannot be completed");

        // A verified late result can resolve an interrupted update after the CP TTL.
        var resolvesUnknown = command.Type == NodeCommandType.UpdateNodeLatest
            && command.ResultCode is "expired_outcome_unknown" or "outcome_unknown"
            && (success && resultCode == "updated_healthy" || resultCode == "rolled_back_healthy");
        if (!resolvesUnknown && command.Status is (NodeCommandStatus.Succeeded or NodeCommandStatus.Failed
            or NodeCommandStatus.Expired or NodeCommandStatus.Cancelled or NodeCommandStatus.NodeReturned))
        {
            var alreadySuccess = command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.NodeReturned;
            if (alreadySuccess == success && string.Equals(command.ResultCode, resultCode, StringComparison.Ordinal))
                return;
            throw new ConflictException("command already completed with a different result");
        }

        var now = _clock.UtcNow;
        command.ResultCode = Truncate(resultCode, 64);
        command.ResultMessage = Truncate(resultMessage, 1024);
        command.ProgressPhase = success ? "Completed" : "Failed";
        command.ProgressUpdatedAt = now;

        if (command.Type == NodeCommandType.RebootHost && success)
        {
            var node = await _db.Nodes.FirstAsync(n => n.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false);

            if (!string.IsNullOrWhiteSpace(bootId) &&
                !string.Equals(bootId.Trim(), node.LastBootId, StringComparison.Ordinal))
            {
                command.Status = NodeCommandStatus.Succeeded;
                command.CompletedAt = now;
                node.LastBootId = bootId.Trim();
            }
            else if (command.Status == NodeCommandStatus.Accepted &&
                     !string.IsNullOrWhiteSpace(bootId) &&
                     string.Equals(bootId.Trim(), node.LastBootId, StringComparison.Ordinal))
            {
                command.Status = NodeCommandStatus.Accepted;
            }
            else
            {
                command.Status = NodeCommandStatus.Accepted;
                command.ExpiresAt = now.Add(RebootRunningTtl);
            }
        }
        else if (success)
        {
            command.Status = NodeCommandStatus.Succeeded;
            command.CompletedAt = now;
            if (!string.IsNullOrWhiteSpace(bootId))
            {
                var node = await _db.Nodes.FirstAsync(n => n.NodeId == nodeId, cancellationToken)
                    .ConfigureAwait(false);
                node.LastBootId = bootId.Trim();
            }
        }
        else
        {
            command.Status = NodeCommandStatus.Failed;
            command.CompletedAt = now;
        }

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        if (command.Type == NodeCommandType.UpdateNodeLatest
            && command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.Failed)
        {
            if (ShouldRestoreAdminState(success, command.ResultCode))
            {
                await RestoreAdminStateFromPayloadAsync(command, nodeId, cancellationToken)
                    .ConfigureAwait(false);
            }
            else if (!success)
            {
                // Drain remains; ensure a clear failure code when caller omitted one.
                if (string.IsNullOrWhiteSpace(command.ResultCode)
                    && TryReadDrainEntered(command.PayloadJson))
                {
                    command.ResultCode = "failed_unhealthy_drained";
                    await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
                }
            }
        }

        if (command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.Failed
            or NodeCommandStatus.Accepted)
        {
            await _audit.WriteAsync(new AuditWriteRequest
            {
                Actor = nodeId,
                Action = "node.command.complete",
                EntityType = "NodeCommand",
                EntityId = command.Id.ToString("N"),
                Detail =
                    $"{{\"status\":\"{command.Status}\",\"success\":{(success ? "true" : "false")},\"code\":\"{Escape(command.ResultCode)}\"}}"
            }, cancellationToken).ConfigureAwait(false);
        }
    }

    public async Task ExpireStaleAsync(CancellationToken cancellationToken = default)
    {
        var nodes = await _db.NodeCommands.AsNoTracking().Where(c => c.ExpiresAt <= _clock.UtcNow
            && (c.Status == NodeCommandStatus.Pending || c.Status == NodeCommandStatus.Claimed
                || c.Status == NodeCommandStatus.Running || c.Status == NodeCommandStatus.Executing
                || c.Status == NodeCommandStatus.Accepted)).Select(c => c.NodeId).Distinct().ToListAsync(cancellationToken);
        foreach (var nodeId in nodes)
            await WithLocationLockAsync(nodeId, async () => { await ExpireStaleForNodeAsync(nodeId, cancellationToken); return true; }, cancellationToken);
    }

    private async Task ExpireStaleForNodeAsync(string nodeId, CancellationToken cancellationToken)
    {
        var now = _clock.UtcNow;
        var stale = await _db.NodeCommands
            .Where(c => c.NodeId == nodeId && c.ExpiresAt <= now &&
                        (c.Status == NodeCommandStatus.Pending
                         || c.Status == NodeCommandStatus.Claimed
                         || c.Status == NodeCommandStatus.Running
                         || c.Status == NodeCommandStatus.Executing
                         || c.Status == NodeCommandStatus.Accepted))
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        if (stale.Count == 0)
            return;

        foreach (var c in stale)
        {
            var priorStatus = c.Status;
            var drainEntered = c.Type == NodeCommandType.UpdateNodeLatest
                               && TryReadDrainEntered(c.PayloadJson);
            var hasSnapshot = c.Type == NodeCommandType.UpdateNodeLatest
                              && TryReadAdminSnapshot(c.PayloadJson, out _, out _, out _);

            if (drainEntered && hasSnapshot)
            {
                // Drain applied; outcome unknown — leave node drained.
                c.Status = NodeCommandStatus.Failed;
                c.CompletedAt ??= now;
                c.ResultCode = "expired_outcome_unknown";
                c.ResultMessage ??=
                    "command TTL exceeded after drain entered; node left drained (outcome unknown)";
                continue;
            }

            c.Status = NodeCommandStatus.Expired;
            c.CompletedAt ??= now;

            if (c.Type == NodeCommandType.UpdateNodeLatest
                && hasSnapshot
                && priorStatus is NodeCommandStatus.Pending or NodeCommandStatus.Claimed
                && !drainEntered)
            {
                c.ResultCode = "expired_no_mutation";
                c.ResultMessage ??= "command TTL exceeded before drain; admin state restored";
                await RestoreAdminStateFromPayloadAsync(c, c.NodeId, cancellationToken)
                    .ConfigureAwait(false);
            }
            else
            {
                c.ResultCode ??= "expired";
                c.ResultMessage ??= "command TTL exceeded";
            }
        }

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    /// <summary>
    /// Whether UpdateNodeLatest completion should restore admin_state_before (including prior drain).
    /// Success implies updated_healthy; listed result codes are safe no-mutation / rollback paths.
    /// Generic failures (health_failed, version_not_confirmed, rollback_failed, outcome_unknown) do not restore.
    /// </summary>
    public static bool ShouldRestoreAdminState(bool success, string? resultCode)
    {
        if (success && resultCode == "updated_healthy") return true;
        if (string.IsNullOrWhiteSpace(resultCode))
            return false;
        if (SafeRestoreResultCodes.Contains(resultCode.Trim()))
            return true;
        return false;
    }

    private async Task PrepareUpdateDrainAsync(
        NodeCommand command,
        string nodeId,
        CancellationToken cancellationToken)
    {
        var node = await _db.Nodes.FirstAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        var cfg = await _db.NodeConfigs.FirstAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);

        var beforeEnabled = cfg.Enabled;
        var beforeDraining = cfg.Draining;
        var beforeMaintenance = cfg.MaintenanceMode;

        MergePayloadAdminSnapshot(
            command, beforeEnabled, beforeDraining, beforeMaintenance,
            drainTimedOut: false, drainEntered: false);

        // Stop accepting new sessions; do not blindly clear prior manual drain/maintenance.
        cfg.Draining = true;
        node.Draining = true;
        cfg.ConfigVersion = checked(cfg.ConfigVersion + 1);
        cfg.UpdatedAt = _clock.UtcNow;
        node.ConfigVersion = cfg.ConfigVersion;
        node.UpdatedAt = cfg.UpdatedAt;
        MergePayloadAdminSnapshot(
            command, beforeEnabled, beforeDraining, beforeMaintenance,
            drainTimedOut: false, drainEntered: true);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        command.ProgressPhase = "Draining";
        command.ProgressUpdatedAt = _clock.UtcNow;
        command.ProgressMessage = "Waiting for a fresh heartbeat and session drain";
        await _db.SaveChangesAsync(cancellationToken);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = nodeId,
            Action = "node.command.update.drain",
            EntityType = "NodeCommand",
            EntityId = command.Id.ToString("N"),
            Detail =
                $"{{\"sessions\":{node.CurrentSessions},\"drain_timed_out\":false}}"
        }, cancellationToken).ConfigureAwait(false);
    }

    private async Task RestoreAdminStateFromPayloadAsync(
        NodeCommand command,
        string nodeId,
        CancellationToken cancellationToken)
    {
        if (!TryReadAdminSnapshot(command.PayloadJson, out var enabled, out var draining, out var maintenance))
            return;

        var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        var cfg = await _db.NodeConfigs.FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        if (node is null || cfg is null)
            return;
        if (node.LifecycleState != NodeLifecycleState.Active) return;

        cfg.Enabled = enabled;
        cfg.Draining = draining;
        cfg.MaintenanceMode = maintenance;
        node.Enabled = enabled;
        node.Draining = draining;
        cfg.ConfigVersion = checked(cfg.ConfigVersion + 1);
        cfg.UpdatedAt = _clock.UtcNow;
        node.ConfigVersion = cfg.ConfigVersion;
        node.UpdatedAt = cfg.UpdatedAt;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = nodeId,
            Action = "node.command.update.restore_admin_state",
            EntityType = "NodeCommand",
            EntityId = command.Id.ToString("N"),
            Detail =
                $"{{\"enabled\":{(enabled ? "true" : "false")},\"draining\":{(draining ? "true" : "false")},\"maintenance_mode\":{(maintenance ? "true" : "false")}}}"
        }, cancellationToken).ConfigureAwait(false);
    }

    private static void MergePayloadAdminSnapshot(
        NodeCommand command,
        bool enabled,
        bool draining,
        bool maintenance,
        bool drainTimedOut,
        bool drainEntered)
    {
        JsonObject root;
        try
        {
            root = string.IsNullOrWhiteSpace(command.PayloadJson)
                ? new JsonObject()
                : JsonNode.Parse(command.PayloadJson)?.AsObject() ?? new JsonObject();
        }
        catch (JsonException)
        {
            root = new JsonObject();
        }

        root["admin_state_before"] = new JsonObject
        {
            ["enabled"] = enabled,
            ["draining"] = draining,
            ["maintenance_mode"] = maintenance
        };
        root["drain_timed_out"] = drainTimedOut;
        root["drain_entered"] = drainEntered;
        command.PayloadJson = root.ToJsonString();
    }

    public static bool TryReadDrainEntered(string? payloadJson)
    {
        if (string.IsNullOrWhiteSpace(payloadJson))
            return false;
        try
        {
            using var doc = JsonDocument.Parse(payloadJson);
            if (!doc.RootElement.TryGetProperty("drain_entered", out var d))
                return false;
            return d.ValueKind == JsonValueKind.True;
        }
        catch (JsonException)
        {
            return false;
        }
    }

    public static bool TryReadAdminSnapshot(
        string? payloadJson,
        out bool enabled,
        out bool draining,
        out bool maintenance)
    {
        enabled = true;
        draining = false;
        maintenance = false;
        if (string.IsNullOrWhiteSpace(payloadJson))
            return false;
        try
        {
            using var doc = JsonDocument.Parse(payloadJson);
            if (!doc.RootElement.TryGetProperty("admin_state_before", out var before)
                || before.ValueKind != JsonValueKind.Object)
                return false;
            if (!before.TryGetProperty("enabled", out var e) || e.ValueKind is not (JsonValueKind.True or JsonValueKind.False)
                || !before.TryGetProperty("draining", out var d) || d.ValueKind is not (JsonValueKind.True or JsonValueKind.False)
                || !before.TryGetProperty("maintenance_mode", out var m) || m.ValueKind is not (JsonValueKind.True or JsonValueKind.False))
                return false;
            enabled = e.GetBoolean();
            draining = d.GetBoolean();
            maintenance = m.GetBoolean();
            return true;
        }
        catch (JsonException)
        {
            return false;
        }
    }

    private async Task AssertLocationDisruptionAllowedAsync(
        Node target,
        Guid? excludeCommandId,
        CancellationToken cancellationToken)
    {
        var now = _clock.UtcNow;
        var locationId = target.LocationId;

        var activeDisruptive = await _db.NodeCommands
            .AsNoTracking()
            .Where(c => DisruptiveTypes.Contains(c.Type)
                        && (c.Status == NodeCommandStatus.Pending
                            || c.Status == NodeCommandStatus.Claimed
                            || c.Status == NodeCommandStatus.Running
                            || c.Status == NodeCommandStatus.Executing
                            || c.Status == NodeCommandStatus.Accepted
                            || c.ResultCode == "expired_outcome_unknown"
                            || c.ResultCode == "outcome_unknown"
                            || c.ResultCode == "rollback_failed")
                        )
            .Join(_db.Nodes.AsNoTracking(),
                c => c.NodeId,
                n => n.NodeId,
                (c, n) => new { c.Id, n.LocationId, c.NodeId })
            .Where(x => x.LocationId == locationId
                        && (excludeCommandId == null || x.Id != excludeCommandId.Value))
            .AnyAsync(cancellationToken)
            .ConfigureAwait(false);

        if (activeDisruptive)
            throw new ConflictException(
                "another disruptive command is already active in this location; wait for it to finish");

        var siblings = await _db.Nodes.AsNoTracking()
            .Where(n => n.LocationId == locationId && n.NodeId != target.NodeId)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var configs = await _db.NodeConfigs.AsNoTracking()
            .Where(c => siblings.Select(s => s.NodeId).Contains(c.NodeId))
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);
        var configById = configs.ToDictionary(c => c.NodeId, StringComparer.Ordinal);

        var eligibleSibling = siblings.Any(s =>
        {
            configById.TryGetValue(s.NodeId, out var cfg);
            return IsEligibleHealthySibling(s, cfg, now);
        });

        if (!eligibleSibling)
            throw new ConflictException(
                "refusing disruptive command: this is the last eligible healthy node in the location");
    }

    internal static bool IsEligibleHealthySibling(Node node, NodeConfig? cfg, DateTime utcNow)
    {
        if (node.LifecycleState != NodeLifecycleState.Active)
            return false;
        if (!node.Enabled || node.Draining || node.TestOnly)
            return false;
        if (node.Capacity <= node.CurrentSessions || (cfg is not null && cfg.Capacity <= node.CurrentSessions))
            return false;
        if (cfg is { MaintenanceMode: true } or { Draining: true } or { Enabled: false })
            return false;
        if (node.Status != NodeRuntimeStatus.Healthy)
            return false;
        if (node.LastSeenAt is null || utcNow - node.LastSeenAt.Value > HeartbeatFreshness)
            return false;
        return true;
    }

    private static bool IsDisruptive(NodeCommandType type) =>
        type is NodeCommandType.UpdateNodeLatest
            or NodeCommandType.RestartNyxveilService
            or NodeCommandType.RebootHost;

    private async Task<T> WithLocationLockAsync<T>(string nodeId, Func<Task<T>> action, CancellationToken ct)
    {
        var location = await _db.Nodes.AsNoTracking().Where(n => n.NodeId == nodeId)
            .Select(n => n.LocationId).FirstOrDefaultAsync(ct) ?? throw new NotFoundException("node not found");
        await using var lease = await ManagementOperationLock.AcquireAsync(_db, "location:" + location, ct);
        // Fresh state is essential for scoped services reused by tests and maintenance workers.
        foreach (var entry in _db.ChangeTracker.Entries().Where(e => e.State == EntityState.Unchanged).ToArray())
            entry.State = EntityState.Detached;
        var currentLocation = await _db.Nodes.AsNoTracking().Where(n => n.NodeId == nodeId)
            .Select(n => n.LocationId).SingleAsync(ct);
        if (currentLocation != location) throw new ConflictException("node location changed; retry");
        var result = await action();
        await lease.CommitAsync(ct);
        return result;
    }

    private async Task<NodeCommand> GetOwnedCommandAsync(
        Guid id,
        string nodeId,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        var command = await _db.NodeCommands.FirstOrDefaultAsync(c => c.Id == id, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("command not found");

        if (!string.Equals(command.NodeId, nodeId, StringComparison.Ordinal))
            throw new ForbiddenException("command belongs to a different node");

        return command;
    }

    internal static void AssertCanEnqueue(NodeCommandType type, IEnumerable<string> roles)
    {
        var roleSet = new HashSet<string>(
            roles ?? Array.Empty<string>(),
            StringComparer.OrdinalIgnoreCase);

        if (roleSet.Contains(AdminRole.ReadOnly) &&
            !roleSet.Contains(AdminRole.Operator) &&
            !roleSet.Contains(AdminRole.SuperAdmin))
            throw new ForbiddenException("ReadOnly cannot enqueue node commands");

        if (roleSet.Contains(AdminRole.SuperAdmin))
            return;

        if (roleSet.Contains(AdminRole.Operator))
        {
            if (type is NodeCommandType.RebootHost or NodeCommandType.UpdateNodeLatest)
                throw new ForbiddenException("Operator cannot enqueue " + type);
            return;
        }

        throw new ForbiddenException("insufficient role to enqueue node commands");
    }

    private static string? Truncate(string? value, int max) =>
        value is null ? null : (value.Length <= max ? value : value[..max]);

    private static string Escape(string? value) =>
        (value ?? string.Empty).Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal);
}
