using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>
/// Heartbeat updates ONLY dynamic health fields (Frozen Core UpdateNodeHealth semantics).
/// Never mutates admin/static config. Always uses Control Plane receive time for LastSeen.
/// </summary>
public sealed class NodeHeartbeatService : INodeHeartbeatService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;

    public NodeHeartbeatService(ControlPlaneDbContext db, IClock clock)
    {
        _db = db;
        _clock = clock;
    }

    public async Task<NodeHeartbeatResponse> ProcessHeartbeatAsync(
        NodeHeartbeatRequest request,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(request.NodeId))
            throw new ValidationException("node_id is required");

        var node = await _db.Nodes.FirstOrDefaultAsync(n => n.NodeId == request.NodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node not found");

        var cfg = await _db.NodeConfigs.AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == node.NodeId, cancellationToken)
            .ConfigureAwait(false);

        // Authoritative server receive time — ignore client-supplied Timestamp for health.
        var now = _clock.UtcNow;

        node.CurrentSessions = Math.Max(0, request.CurrentSessions);
        node.LastSeenAt = now;
        node.UpdatedAt = now;

        // Runtime capacity may be reported, but never exceeds admin-configured NodeConfig.Capacity.
        if (request.Capacity > 0)
        {
            var configured = cfg?.Capacity ?? node.Capacity;
            node.Capacity = Math.Min(request.Capacity, configured);
        }

        // Do NOT set Status=Healthy (would exit maintenance semantics / fight health worker).
        // Do NOT update ServerVersion / Enabled / Draining / Location / identity / ConfigVersion.

        var health = await _db.NodeHealth.FirstOrDefaultAsync(h => h.NodeId == node.NodeId, cancellationToken)
            .ConfigureAwait(false);
        if (health is null)
        {
            health = new NodeHealth { NodeId = node.NodeId };
            _db.NodeHealth.Add(health);
        }

        health.CpuPercent = ClampPercent(request.CpuUsage ?? request.Load);
        health.MemoryPercent = ClampPercent(request.MemoryUsage ?? 0);
        health.MemoryBytes = request.MemoryBytes;
        health.UptimeSeconds = request.Uptime;
        health.ActiveSessions = Math.Max(0, request.CurrentSessions);
        health.NetworkRxRate = request.NetworkRxRate;
        health.NetworkTxRate = request.NetworkTxRate;
        health.LoadAverage = request.Load;
        health.Healthy = request.Healthy ?? true;
        health.UpdatedAt = now;

        _db.NodeMetrics.Add(new NodeMetric
        {
            Id = Guid.NewGuid(),
            NodeId = node.NodeId,
            Timestamp = now,
            CpuPercent = health.CpuPercent,
            MemoryPercent = health.MemoryPercent,
            MemoryBytes = health.MemoryBytes,
            ActiveSessions = health.ActiveSessions,
            NetworkRxRate = health.NetworkRxRate,
            NetworkTxRate = health.NetworkTxRate,
            UptimeSeconds = health.UptimeSeconds
        });

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        var configVersion = cfg?.ConfigVersion ?? node.ConfigVersion;
        return new NodeHeartbeatResponse
        {
            Accepted = true,
            Status = node.Status.ToString().ToLowerInvariant(),
            ConfigVersion = configVersion
        };
    }

    private static double ClampPercent(double v) => Math.Clamp(v, 0, 100);
}
