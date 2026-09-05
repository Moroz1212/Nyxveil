using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

/// <summary>
/// Marks nodes Degraded/Offline based on last heartbeat age vs <see cref="NodeHeartbeatOptions"/>.
/// </summary>
public sealed class NodeHealthEvaluationWorker : BackgroundService
{
    private readonly IServiceScopeFactory _scopeFactory;
    private readonly IOptionsMonitor<NodeHeartbeatOptions> _options;
    private readonly ILogger<NodeHealthEvaluationWorker> _logger;

    public NodeHealthEvaluationWorker(
        IServiceScopeFactory scopeFactory,
        IOptionsMonitor<NodeHeartbeatOptions> options,
        ILogger<NodeHealthEvaluationWorker> logger)
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
                await EvaluateAsync(stoppingToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _logger.LogError(ex, "Node health evaluation failed");
            }

            await Task.Delay(TimeSpan.FromSeconds(Math.Max(15, _options.CurrentValue.IntervalSeconds)), stoppingToken)
                .ConfigureAwait(false);
        }
    }

    private async Task EvaluateAsync(CancellationToken cancellationToken)
    {
        var opts = _options.CurrentValue;
        await using var scope = _scopeFactory.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var clock = scope.ServiceProvider.GetRequiredService<IClock>();
        var now = clock.UtcNow;

        var nodes = await db.Nodes.ToListAsync(cancellationToken).ConfigureAwait(false);

        var changed = 0;
        foreach (var node in nodes)
        {
            // Runtime health only — MaintenanceMode lives on NodeConfig and is never rewritten here.
            var ageSeconds = node.LastSeenAt.HasValue
                ? (now - node.LastSeenAt.Value).TotalSeconds
                : double.MaxValue;

            NodeRuntimeStatus next;
            if (ageSeconds >= opts.OfflineAfterSeconds)
            {
                next = NodeRuntimeStatus.Offline;
            }
            else if (ageSeconds >= opts.DegradedAfterSeconds)
            {
                next = NodeRuntimeStatus.Degraded;
            }
            else
            {
                next = NodeRuntimeStatus.Healthy;
            }

            if (node.Status != next)
            {
                node.Status = next;
                node.HealthStatus = next.ToString();
                node.UpdatedAt = now;
                changed++;
            }
        }

        if (changed > 0)
        {
            await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            _logger.LogInformation("Updated health status for {Count} node(s)", changed);
        }
    }
}
