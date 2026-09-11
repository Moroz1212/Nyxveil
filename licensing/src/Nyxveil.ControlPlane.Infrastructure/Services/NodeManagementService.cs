using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>
/// Authoritative node config mutations. Increments <see cref="NodeConfig.ConfigVersion"/> once per managed change
/// and mirrors Enabled/Draining/Capacity/ConfigVersion onto <see cref="Node"/> for catalog queries.
/// </summary>
public sealed class NodeManagementService : INodeManagementService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;
    private readonly IAuditService _audit;
    private readonly ICriticalOperationAuthorizer _criticalOps;

    public NodeManagementService(
        ControlPlaneDbContext db,
        IClock clock,
        IAuditService audit,
        ICriticalOperationAuthorizer? criticalOps = null)
    {
        _db = db;
        _clock = clock;
        _audit = audit;
        _criticalOps = criticalOps ?? AllowAllCriticalOperationAuthorizer.Instance;
    }

    public Task SetEnabledAsync(string nodeId, bool enabled, string actor, CancellationToken cancellationToken = default) =>
        MutateAsync(nodeId, actor, enabled ? "node.enabled" : "node.disabled", (node, cfg) =>
        {
            cfg.Enabled = enabled;
            node.Enabled = enabled;
        }, cancellationToken);

    public Task SetDrainingAsync(string nodeId, bool draining, string actor, CancellationToken cancellationToken = default) =>
        MutateAsync(nodeId, actor, "node.draining", (node, cfg) =>
        {
            cfg.Draining = draining;
            node.Draining = draining;
        }, cancellationToken);

    public Task EnterMaintenanceAsync(string nodeId, string actor, CancellationToken cancellationToken = default) =>
        MutateAsync(nodeId, actor, "node.maintenance.enter", (_, cfg) =>
        {
            cfg.MaintenanceMode = true;
        }, cancellationToken);

    public Task ExitMaintenanceAsync(string nodeId, string actor, CancellationToken cancellationToken = default) =>
        MutateAsync(nodeId, actor, "node.maintenance.exit", (_, cfg) =>
        {
            cfg.MaintenanceMode = false;
        }, cancellationToken);

    public Task SetCapacityAsync(string nodeId, int capacity, string actor, CancellationToken cancellationToken = default)
    {
        if (capacity < 0)
            throw new ValidationException("capacity must be >= 0");

        return MutateAsync(nodeId, actor, "node.capacity", (node, cfg) =>
        {
            cfg.Capacity = capacity;
            // Effective capacity cannot exceed configured admin max.
            if (node.Capacity > capacity)
                node.Capacity = capacity;
            else if (node.Capacity <= 0)
                node.Capacity = capacity;
        }, cancellationToken);
    }

    public Task SetTestOnlyAsync(string nodeId, bool testOnly, string actor, CancellationToken cancellationToken = default) =>
        MutateAsync(nodeId, actor, "node.testonly", (node, _) =>
        {
            node.TestOnly = testOnly;
        }, cancellationToken);

    public async Task ChangeLocationAsync(
        string nodeId,
        string locationIdOrCode,
        string actor,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(locationIdOrCode))
            throw new ValidationException("location is required");

        var all = await _db.Locations.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);
        var canonical = LocationIdResolver.ResolveCanonicalId(all, locationIdOrCode)
                        ?? throw new NotFoundException("location not found");
        var loc = all.First(l => string.Equals(l.LocationId, canonical, StringComparison.Ordinal));
        if (!loc.Enabled)
            throw new ValidationException("target location is disabled");

        await MutateAsync(nodeId, actor, "node.location.changed", (node, _) =>
        {
            node.LocationId = canonical;
        }, cancellationToken).ConfigureAwait(false);
    }

    public async Task<NodeConfigResponse> GetAuthoritativeConfigAsync(
        string nodeId,
        CancellationToken cancellationToken = default)
    {
        var cfg = await _db.NodeConfigs.AsNoTracking().Include(c => c.Node)
            .FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node config not found");

        return ToResponse(cfg);
    }

    public async Task<NodeAdminStatusResponse> GetAdminStatusAsync(
        string nodeId,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        var node = await _db.Nodes.AsNoTracking()
            .FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node not found");
        var cfg = await _db.NodeConfigs.AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);

        var maintenance = cfg?.MaintenanceMode ?? false;
        var draining = node.Draining || (cfg?.Draining ?? false);
        var enabled = node.Enabled && (cfg?.Enabled ?? true);
        var now = _clock.UtcNow;
        var online = node.LastSeenAt is not null
                     && now - node.LastSeenAt.Value <= NodeCommandService.HeartbeatFreshness;
        var healthy = node.Status == NodeRuntimeStatus.Healthy && online;
        var accepting = enabled && !draining && !maintenance
                        && node.LifecycleState == NodeLifecycleState.Active
                        && healthy;

        return new NodeAdminStatusResponse
        {
            NodeId = node.NodeId,
            LocationId = node.LocationId,
            Enabled = enabled,
            Draining = draining,
            MaintenanceMode = maintenance,
            Healthy = healthy,
            Accepting = accepting,
            Online = online,
            LastSeenAt = node.LastSeenAt,
            CurrentSessions = node.CurrentSessions,
            ReportedServerVersion = string.IsNullOrWhiteSpace(node.ReportedServerVersion)
                ? node.ServerVersion
                : node.ReportedServerVersion,
            ConfigVersion = node.ConfigVersion,
            LifecycleState = node.LifecycleState.ToString()
        };
    }

    public async Task<NodeDecommissionPreview> GetDecommissionPreviewAsync(
        string nodeId,
        CancellationToken cancellationToken = default)
    {
        var node = await _db.Nodes.AsNoTracking()
            .FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken).ConfigureAwait(false)
            ?? throw new NotFoundException("node not found");
        var endpoints = await _db.NodeEndpoints.CountAsync(e => e.NodeId == nodeId, cancellationToken).ConfigureAwait(false);
        var metrics = await _db.NodeMetrics.CountAsync(m => m.NodeId == nodeId, cancellationToken).ConfigureAwait(false);
        return new NodeDecommissionPreview
        {
            NodeId = nodeId,
            CurrentSessions = node.CurrentSessions,
            EndpointCount = endpoints,
            MetricCount = metrics,
            ImpactSummary = node.CurrentSessions > 0
                ? $"{node.CurrentSessions} active session(s) must be drained; {endpoints} endpoint(s) will leave the catalog."
                : $"No active sessions; {endpoints} endpoint(s) will leave the catalog. Historical metrics retained: {metrics}."
        };
    }

    public Task SoftDeleteAsync(
        string nodeId, string actor, string? reason, bool force = false,
        CancellationToken cancellationToken = default) =>
        DecommissionAsync(nodeId, actor, reason, NodeLifecycleState.Deleted, "node.deleted", force, cancellationToken);

    public Task RevokeAsync(
        string nodeId, string actor, string? reason, bool force = false,
        CancellationToken cancellationToken = default) =>
        DecommissionAsync(nodeId, actor, reason, NodeLifecycleState.Revoked, "node.revoked", force, cancellationToken);

    private async Task DecommissionAsync(
        string nodeId,
        string actor,
        string? reason,
        NodeLifecycleState state,
        string auditAction,
        bool force,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        // Soft-delete and revoke remove production capacity / trust; require fresh step-up.
        _criticalOps.AssertAllowed(CriticalOperation.DeleteNode);

        await using var tx = await LockLocationAsync(nodeId, cancellationToken);
        try
        {
            var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false) ?? throw new NotFoundException("node not found");
            var cfg = await _db.NodeConfigs.FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false) ?? throw new NotFoundException("node config not found");
            if (node.CurrentSessions > 0 && !force)
                throw new ValidationException("node has active sessions; drain first or use force");

            var now = _clock.UtcNow;
            node.Enabled = false;
            node.Draining = true;
            node.LifecycleState = state;
            node.DeletedAt = now;
            node.DeletedBy = string.IsNullOrWhiteSpace(actor) ? "admin" : actor;
            node.DeletionReason = string.IsNullOrWhiteSpace(reason) ? null : reason.Trim();
            cfg.Enabled = false;
            cfg.Draining = true;
            cfg.ConfigVersion = checked(cfg.ConfigVersion + 1);
            cfg.UpdatedAt = now;
            node.ConfigVersion = cfg.ConfigVersion;
            node.UpdatedAt = now;

            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            await _audit.WriteAsync(new AuditWriteRequest
            {
                Actor = node.DeletedBy,
                Action = auditAction,
                EntityType = "Node",
                EntityId = nodeId,
                Detail = $"reason={node.DeletionReason ?? "unspecified"}; force={force}; config_version={cfg.ConfigVersion}"
            }, cancellationToken).ConfigureAwait(false);
            await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            await tx.RollbackAsync(cancellationToken).ConfigureAwait(false);
            _db.ChangeTracker.Clear();
            throw;
        }
    }

    private async Task<ManagementOperationLock> LockLocationAsync(string nodeId, CancellationToken ct)
    {
        var location = await _db.Nodes.AsNoTracking().Where(n => n.NodeId == nodeId)
            .Select(n => n.LocationId).SingleOrDefaultAsync(ct) ?? throw new NotFoundException("node not found");
        var lease = await ManagementOperationLock.AcquireAsync(_db, "location:" + location, ct);
        try
        {
            if (await _db.NodeCommands.AsNoTracking().AnyAsync(c => c.Node.LocationId == location
                && (c.Status == NodeCommandStatus.Pending || c.Status == NodeCommandStatus.Claimed
                    || c.Status == NodeCommandStatus.Running || c.Status == NodeCommandStatus.Executing
                    || c.Status == NodeCommandStatus.Accepted), ct))
                throw new ConflictException("location has an active management command; wait for completion before changing node configuration");
            return lease;
        }
        catch { await lease.DisposeAsync(); throw; }
    }

    private async Task MutateAsync(
        string nodeId,
        string actor,
        string auditAction,
        Action<Node, NodeConfig> apply,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        await using var tx = await LockLocationAsync(nodeId, cancellationToken);

        try
        {
            var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false)
                ?? throw new NotFoundException("node not found");
            if (node.LifecycleState == NodeLifecycleState.Deleted)
                throw new ForbiddenException("deleted nodes cannot be mutated");

            var cfg = await _db.NodeConfigs.FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false)
                ?? throw new NotFoundException("node config not found");

            apply(node, cfg);

            var now = _clock.UtcNow;
            cfg.ConfigVersion = checked(cfg.ConfigVersion + 1);
            cfg.UpdatedAt = now;

            // Projection mirrors for catalog / list queries.
            node.Enabled = cfg.Enabled;
            node.Draining = cfg.Draining;
            node.ConfigVersion = cfg.ConfigVersion;
            if (node.Capacity > cfg.Capacity)
                node.Capacity = cfg.Capacity;
            node.UpdatedAt = now;

            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            await _audit.WriteAsync(new AuditWriteRequest
            {
                Actor = string.IsNullOrWhiteSpace(actor) ? "admin" : actor,
                Action = auditAction,
                EntityType = "Node",
                EntityId = nodeId,
                Detail = $"config_version={cfg.ConfigVersion}"
            }, cancellationToken).ConfigureAwait(false);
            await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (DbUpdateConcurrencyException)
        {
            await tx.RollbackAsync(cancellationToken).ConfigureAwait(false);
            _db.ChangeTracker.Clear();
            throw new ConflictException("node config changed concurrently; reload and retry");
        }
        catch
        {
            await tx.RollbackAsync(cancellationToken).ConfigureAwait(false);
            _db.ChangeTracker.Clear();
            throw;
        }
    }

    internal static NodeConfigResponse ToResponse(NodeConfig cfg) => new()
    {
        NodeId = cfg.NodeId,
        LocationId = cfg.Node.LocationId,
        Enabled = cfg.Enabled,
        Draining = cfg.Draining,
        MaintenanceMode = cfg.MaintenanceMode,
        TransportPolicyJson = cfg.TransportPolicyJson,
        EchPolicyJson = cfg.EchPolicyJson,
        Mtu = cfg.Mtu,
        Capacity = cfg.Capacity,
        MinimumServerVersion = cfg.MinimumServerVersion,
        MinimumProtocolVersion = cfg.MinimumProtocolVersion,
        ConfigVersion = cfg.ConfigVersion,
        // Treat unspecified DB DateTime as UTC wall time (no host-local conversion).
        UpdatedAt = new DateTimeOffset(
            cfg.UpdatedAt.Kind == DateTimeKind.Unspecified
                ? DateTime.SpecifyKind(cfg.UpdatedAt, DateTimeKind.Utc)
                : cfg.UpdatedAt.ToUniversalTime())
    };
}
