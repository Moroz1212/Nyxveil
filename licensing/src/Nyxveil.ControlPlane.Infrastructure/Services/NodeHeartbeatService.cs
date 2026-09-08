using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
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
        if (node.LifecycleState is NodeLifecycleState.Deleted or NodeLifecycleState.Revoked)
            throw new ForbiddenException("node deleted/revoked; operator re-approval required");

        var cfg = await _db.NodeConfigs.AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == node.NodeId, cancellationToken)
            .ConfigureAwait(false);

        // Authoritative server receive time — ignore client-supplied Timestamp for health.
        var now = _clock.UtcNow;

        node.CurrentSessions = Math.Max(0, request.CurrentSessions);
        node.LastSeenAt = now;
        node.UpdatedAt = now;
        if (request.TlsMode is not null) node.TlsMode = request.TlsMode.Trim();
        if (request.CertSubject is not null) node.CertSubject = request.CertSubject.Trim();
        if (request.CertIssuer is not null) node.CertIssuer = request.CertIssuer.Trim();
        if (request.CertSan is not null) node.CertSan = request.CertSan.Trim();
        if (request.CertNotBefore.HasValue) node.CertNotBefore = request.CertNotBefore;
        if (request.CertNotAfter.HasValue) node.CertNotAfter = request.CertNotAfter;
        if (request.CertThumbprint is not null) node.CertThumbprint = request.CertThumbprint.Trim();
        if (request.AcmeAutoRenew.HasValue) node.AcmeAutoRenew = request.AcmeAutoRenew.Value;
        if (request.LastRenewalAttempt.HasValue) node.LastRenewalAttempt = request.LastRenewalAttempt;
        if (request.LastRenewalSuccess.HasValue) node.LastSuccessfulRenewal = request.LastRenewalSuccess;
        if (request.LastRenewalNext.HasValue) node.NextPlannedRenewal = request.LastRenewalNext;
        if (request.LastRenewalError is not null) node.LastRenewalError = request.LastRenewalError.Trim();
        if (request.ManagementCapabilities is not null)
            node.ManagementCapabilities = Truncate(request.ManagementCapabilities.Trim(), 512);
        if (request.BootId is not null)
            node.LastBootId = Truncate(request.BootId.Trim(), 128);
        if (request.SupportsCommands.HasValue)
            node.SupportsNodeCommands = request.SupportsCommands.Value;
        if (!string.IsNullOrWhiteSpace(request.Version))
        {
            node.ReportedServerVersion = Truncate(request.Version.Trim(), 64);
            node.VersionReportedAt = now;
        }

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
        if (request.TunReady.HasValue) health.TunReady = request.TunReady;
        if (request.TlsOk.HasValue) health.TlsOk = request.TlsOk;
        if (request.QuicOk.HasValue) health.QuicOk = request.QuicOk;
        if (request.BridgeOk.HasValue) health.BridgeOk = request.BridgeOk;
        if (request.TicketKeysLoaded.HasValue) health.TicketKeysLoaded = request.TicketKeysLoaded;
        if (request.RevocationStale.HasValue) health.RevocationStale = request.RevocationStale;
        if (request.CpConnected.HasValue) health.CpConnected = request.CpConnected;
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

    private static string? Truncate(string? value, int max) =>
        value is null ? null : (value.Length <= max ? value : value[..max]);
}
