using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

/// <summary>
/// Convenience status flip for expired licenses. Auth/ticket paths still enforce ExpiresAt independently.
/// </summary>
public sealed class LicenseExpirationWorker : BackgroundService
{
    private static readonly TimeSpan Interval = TimeSpan.FromMinutes(5);

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly ILogger<LicenseExpirationWorker> _logger;

    public LicenseExpirationWorker(
        IServiceScopeFactory scopeFactory,
        ILogger<LicenseExpirationWorker> logger)
    {
        _scopeFactory = scopeFactory;
        _logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await ExpireAsync(stoppingToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _logger.LogError(ex, "License expiration sweep failed");
            }

            await Task.Delay(Interval, stoppingToken).ConfigureAwait(false);
        }
    }

    private async Task ExpireAsync(CancellationToken cancellationToken)
    {
        await using var scope = _scopeFactory.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var clock = scope.ServiceProvider.GetRequiredService<IClock>();
        var now = clock.UtcNow;

        var expired = await db.Licenses
            .Where(l =>
                (l.Status == LicenseStatus.Active || l.Status == LicenseStatus.Pending) &&
                l.ExpiresAt != null &&
                l.ExpiresAt < now)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        if (expired.Count == 0)
        {
            return;
        }

        foreach (var license in expired)
        {
            license.Status = LicenseStatus.Expired;
            license.UpdatedAt = now;
        }

        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        _logger.LogInformation("Marked {Count} license(s) expired", expired.Count);
    }
}
