using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
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

    public NodeManagementService(ControlPlaneDbContext db, IClock clock, IAuditService audit)
    {
        _db = db;
        _clock = clock;
        _audit = audit;
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

    private async Task MutateAsync(
        string nodeId,
        string actor,
        string auditAction,
        Action<Node, NodeConfig> apply,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        await using var tx = await _db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);

        try
        {
            var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
                .ConfigureAwait(false)
                ?? throw new NotFoundException("node not found");

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
