using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

/// <summary>Advances sequential location rollouts stored in SystemSettings.</summary>
public sealed class LocationRolloutWorker : BackgroundService
{
    private readonly IServiceScopeFactory _scopes;
    private readonly ILogger<LocationRolloutWorker> _log;

    public LocationRolloutWorker(IServiceScopeFactory scopes, ILogger<LocationRolloutWorker> log)
    {
        _scopes = scopes;
        _log = log;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = _scopes.CreateAsyncScope();
                var rollout = scope.ServiceProvider.GetRequiredService<ILocationRolloutService>();
                await rollout.TickAsync(stoppingToken).ConfigureAwait(false);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _log.LogWarning(ex, "Location rollout tick failed");
            }

            try
            {
                await Task.Delay(TimeSpan.FromSeconds(5), stoppingToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                break;
            }
        }
    }
}
