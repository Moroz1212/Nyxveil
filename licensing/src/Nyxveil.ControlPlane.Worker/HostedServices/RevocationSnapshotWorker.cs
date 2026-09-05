using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

/// <summary>
/// Maintains revocation snapshot version metadata in SystemSettings.
/// Optional cleanup of very old revocation rows can be enabled later without changing the API surface.
/// </summary>
public sealed class RevocationSnapshotWorker : BackgroundService
{
    private const string SnapshotVersionKey = "revocation.snapshot.version";
    private static readonly TimeSpan Interval = TimeSpan.FromMinutes(2);

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly ILogger<RevocationSnapshotWorker> _logger;

    public RevocationSnapshotWorker(
        IServiceScopeFactory scopeFactory,
        ILogger<RevocationSnapshotWorker> logger)
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
                await SyncSnapshotAsync(stoppingToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _logger.LogError(ex, "Revocation snapshot sync failed");
            }

            await Task.Delay(Interval, stoppingToken).ConfigureAwait(false);
        }
    }

    private async Task SyncSnapshotAsync(CancellationToken cancellationToken)
    {
        await using var scope = _scopeFactory.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var clock = scope.ServiceProvider.GetRequiredService<IClock>();

        var maxVersion = await db.Revocations
            .Select(r => (long?)r.Version)
            .MaxAsync(cancellationToken)
            .ConfigureAwait(false) ?? 0L;

        var setting = await db.SystemSettings
            .FirstOrDefaultAsync(s => s.Key == SnapshotVersionKey, cancellationToken)
            .ConfigureAwait(false);

        var currentStored = 0L;
        if (setting is not null && long.TryParse(setting.Value, out var parsed))
        {
            currentStored = parsed;
        }

        if (maxVersion <= currentStored && setting is not null)
        {
            return;
        }

        if (setting is null)
        {
            db.SystemSettings.Add(new Domain.Entities.SystemSetting
            {
                Key = SnapshotVersionKey,
                Value = maxVersion.ToString(),
                UpdatedAt = clock.UtcNow,
                UpdatedBy = "RevocationSnapshotWorker"
            });
        }
        else
        {
            setting.Value = maxVersion.ToString();
            setting.UpdatedAt = clock.UtcNow;
            setting.UpdatedBy = "RevocationSnapshotWorker";
        }

        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        _logger.LogDebug("Revocation snapshot version set to {Version}", maxVersion);
    }
}
