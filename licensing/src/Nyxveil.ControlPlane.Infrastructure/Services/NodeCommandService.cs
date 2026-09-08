using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
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

    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;
    private readonly IAuditService _audit;

    public NodeCommandService(ControlPlaneDbContext db, IClock clock, IAuditService audit)
    {
        _db = db;
        _clock = clock;
        _audit = audit;
    }

    public async Task<NodeCommand> EnqueueAsync(
        string nodeId,
        NodeCommandType type,
        string actor,
        IEnumerable<string> roles,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");
        if (string.IsNullOrWhiteSpace(actor))
            throw new ValidationException("actor is required");

        AssertCanEnqueue(type, roles);

        var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node not found");
        if (node.LifecycleState is NodeLifecycleState.Deleted or NodeLifecycleState.Revoked)
            throw new ForbiddenException("node deleted/revoked");

        var now = _clock.UtcNow;
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
            CorrelationId = Guid.NewGuid()
        };

        _db.NodeCommands.Add(command);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = actor,
            Action = "node.command.enqueue",
            EntityType = "NodeCommand",
            EntityId = command.Id.ToString("N"),
            Detail = $"{{\"node_id\":\"{node.NodeId}\",\"type\":\"{type}\"}}"
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
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        var now = _clock.UtcNow;
        await ExpireStaleAsync(cancellationToken).ConfigureAwait(false);

        var command = await _db.NodeCommands
            .Where(c => c.NodeId == nodeId
                        && c.Status == NodeCommandStatus.Pending
                        && c.ExpiresAt > now)
            .OrderBy(c => c.IssuedAt)
            .FirstOrDefaultAsync(cancellationToken)
            .ConfigureAwait(false);

        if (command is null)
            return null;

        command.Status = NodeCommandStatus.Claimed;
        command.ClaimedAt = now;
        command.AttemptCount += 1;
        // After claim, allow Running TTL (or reboot TTL) before expiry.
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

    public async Task MarkStartedAsync(Guid id, string nodeId, CancellationToken cancellationToken = default)
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
        var command = await GetOwnedCommandAsync(id, nodeId, cancellationToken).ConfigureAwait(false);

        // Idempotent success/failure if already terminal with same outcome.
        if (command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.Failed
            or NodeCommandStatus.Expired or NodeCommandStatus.Cancelled or NodeCommandStatus.NodeReturned)
        {
            var alreadySuccess = command.Status is NodeCommandStatus.Succeeded or NodeCommandStatus.NodeReturned;
            if (alreadySuccess == success)
                return;
            throw new ConflictException("command already completed with a different result");
        }

        var now = _clock.UtcNow;
        command.ResultCode = Truncate(resultCode, 64);
        command.ResultMessage = Truncate(resultMessage, 1024);

        if (command.Type == NodeCommandType.RebootHost && success)
        {
            var node = await _db.Nodes.FirstAsync(n => n.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false);

            // NodeReturned when boot_id differs from last known boot id.
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
                // Still same boot — keep Accepted.
                command.Status = NodeCommandStatus.Accepted;
            }
            else
            {
                // First success report before reboot return.
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
        var now = _clock.UtcNow;
        var stale = await _db.NodeCommands
            .Where(c => c.ExpiresAt <= now &&
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
            c.Status = NodeCommandStatus.Expired;
            c.CompletedAt ??= now;
            c.ResultCode ??= "expired";
            c.ResultMessage ??= "command TTL exceeded";
        }

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
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
