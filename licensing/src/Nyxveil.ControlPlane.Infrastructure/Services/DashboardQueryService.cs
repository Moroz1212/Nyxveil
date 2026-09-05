using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class DashboardQueryService : IDashboardQueryService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;

    public DashboardQueryService(ControlPlaneDbContext db, IClock clock)
    {
        _db = db;
        _clock = clock;
    }

    public async Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default)
    {
        var now = _clock.UtcNow;
        var expiringUntil = now.AddDays(14);

        return new DashboardSummary
        {
            ActiveLicenses = await _db.Licenses.CountAsync(l => l.Status == LicenseStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            ExpiringLicenses = await _db.Licenses.CountAsync(
                    l => l.Status == LicenseStatus.Active && l.ExpiresAt != null && l.ExpiresAt <= expiringUntil,
                    cancellationToken)
                .ConfigureAwait(false),
            RevokedLicenses = await _db.Licenses.CountAsync(l => l.Status == LicenseStatus.Revoked, cancellationToken)
                .ConfigureAwait(false),
            ActiveDevices = await _db.Devices.CountAsync(d => d.Status == DeviceStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            TotalDevices = await _db.Devices.CountAsync(cancellationToken).ConfigureAwait(false),
            HealthyNodes = await _db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Healthy, cancellationToken)
                .ConfigureAwait(false),
            DegradedNodes = await _db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Degraded, cancellationToken)
                .ConfigureAwait(false),
            OfflineNodes = await _db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Offline, cancellationToken)
                .ConfigureAwait(false),
            OnlineNodes = await _db.Nodes.CountAsync(
                    n => n.Status == NodeRuntimeStatus.Healthy || n.Status == NodeRuntimeStatus.Degraded,
                    cancellationToken)
                .ConfigureAwait(false),
            TotalNodes = await _db.Nodes.CountAsync(cancellationToken).ConfigureAwait(false),
            ActiveSessions = await _db.Nodes.SumAsync(n => (int?)n.CurrentSessions, cancellationToken).ConfigureAwait(false) ?? 0,
            PendingBootstrapTokens = await _db.BootstrapTokens.CountAsync(
                    t => t.Status == BootstrapTokenStatus.Active,
                    cancellationToken)
                .ConfigureAwait(false)
        };
    }
}
