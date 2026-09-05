using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

/// <summary>Deletes old <c>NodeMetrics</c> rows per <see cref="MetricsRetentionOptions.RawDays"/>.</summary>
public sealed class MetricsRetentionWorker : BackgroundService
{
    private static readonly TimeSpan Interval = TimeSpan.FromHours(6);

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly IOptionsMonitor<MetricsRetentionOptions> _options;
    private readonly ILogger<MetricsRetentionWorker> _logger;

    public MetricsRetentionWorker(
        IServiceScopeFactory scopeFactory,
        IOptionsMonitor<MetricsRetentionOptions> options,
        ILogger<MetricsRetentionWorker> logger)
    {
        _scopeFactory = scopeFactory;
        _options = options;
        _logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await CleanupAsync(stoppingToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _logger.LogError(ex, "Metrics retention cleanup failed");
            }

            await Task.Delay(Interval, stoppingToken).ConfigureAwait(false);
        }
    }

    private async Task CleanupAsync(CancellationToken cancellationToken)
    {
        var rawDays = Math.Max(1, _options.CurrentValue.RawDays);
        await using var scope = _scopeFactory.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var clock = scope.ServiceProvider.GetRequiredService<IClock>();
        var cutoff = clock.UtcNow.AddDays(-rawDays);
        var now = clock.UtcNow;
        await db.NodeRequestNonces.Where(n => n.ExpiresAt < now).ExecuteDeleteAsync(cancellationToken);

        var deleted = await db.NodeMetrics
            .Where(m => m.Timestamp < cutoff)
            .ExecuteDeleteAsync(cancellationToken)
            .ConfigureAwait(false);

        if (deleted > 0)
        {
            _logger.LogInformation("Deleted {Count} metric row(s) older than {Cutoff:u}", deleted, cutoff);
        }
    }
}
