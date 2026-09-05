using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class DashboardQueryService : IDashboardQueryService
{
    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly IClock _clock;

    public DashboardQueryService(IDbContextFactory<ControlPlaneDbContext> dbFactory, IClock clock)
    {
        _dbFactory = dbFactory;
        _clock = clock;
    }

    public async Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var expiringUntil = now.AddDays(14);

        // Sequential queries on a dedicated context (safe under Blazor circuit concurrency).
        return new DashboardSummary
        {
            ActiveLicenses = await db.Licenses.CountAsync(l => l.Status == LicenseStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            ExpiringLicenses = await db.Licenses.CountAsync(
                    l => l.Status == LicenseStatus.Active && l.ExpiresAt != null && l.ExpiresAt <= expiringUntil,
                    cancellationToken)
                .ConfigureAwait(false),
            RevokedLicenses = await db.Licenses.CountAsync(l => l.Status == LicenseStatus.Revoked, cancellationToken)
                .ConfigureAwait(false),
            ActiveDevices = await db.Devices.CountAsync(d => d.Status == DeviceStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            TotalDevices = await db.Devices.CountAsync(cancellationToken).ConfigureAwait(false),
            HealthyNodes = await db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Healthy, cancellationToken)
                .ConfigureAwait(false),
            DegradedNodes = await db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Degraded, cancellationToken)
                .ConfigureAwait(false),
            OfflineNodes = await db.Nodes.CountAsync(n => n.Status == NodeRuntimeStatus.Offline, cancellationToken)
                .ConfigureAwait(false),
            OnlineNodes = await db.Nodes.CountAsync(
                    n => n.Status == NodeRuntimeStatus.Healthy || n.Status == NodeRuntimeStatus.Degraded,
                    cancellationToken)
                .ConfigureAwait(false),
            TotalNodes = await db.Nodes.CountAsync(cancellationToken).ConfigureAwait(false),
            ActiveSessions = await db.Nodes.SumAsync(n => (int?)n.CurrentSessions, cancellationToken).ConfigureAwait(false) ?? 0,
            PendingBootstrapTokens = await db.BootstrapTokens.CountAsync(
                    t => t.Status == BootstrapTokenStatus.Active,
                    cancellationToken)
                .ConfigureAwait(false)
        };
    }
}
