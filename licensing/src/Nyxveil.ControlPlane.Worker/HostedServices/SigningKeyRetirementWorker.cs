using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.Worker.HostedServices;

public sealed class SigningKeyRetirementWorker : BackgroundService
{
    private readonly IServiceScopeFactory _scopes;
    private readonly ILogger<SigningKeyRetirementWorker> _logger;

    public SigningKeyRetirementWorker(IServiceScopeFactory scopes, ILogger<SigningKeyRetirementWorker> logger)
    {
        _scopes = scopes;
        _logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                await using var scope = _scopes.CreateAsyncScope();
                var n = await scope.ServiceProvider.GetRequiredService<ISigningKeyService>()
                    .FinalizeExpiredRetiringAsync(stoppingToken)
                    .ConfigureAwait(false);
                if (n > 0)
                    _logger.LogInformation("Finalized {Count} retiring signing key(s)", n);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                _logger.LogWarning(ex, "Signing key retirement worker failed");
            }

            await Task.Delay(TimeSpan.FromMinutes(1), stoppingToken).ConfigureAwait(false);
        }
    }
}
