using Microsoft.AspNetCore.Components.Server.Circuits;
using Microsoft.Extensions.Logging;

namespace Nyxveil.ControlPlane.Web.Hosting;

/// <summary>Ensures Blazor circuit failures are written to the same file logger as HTTP errors.</summary>
public sealed class LoggingCircuitHandler : CircuitHandler
{
    private readonly ILogger<LoggingCircuitHandler> _logger;

    public LoggingCircuitHandler(ILogger<LoggingCircuitHandler> logger)
    {
        _logger = logger;
    }

    public override Task OnCircuitOpenedAsync(Circuit circuit, CancellationToken cancellationToken)
    {
        _logger.LogDebug("Blazor circuit opened CircuitId={CircuitId}", circuit.Id);
        return Task.CompletedTask;
    }

    public override Task OnCircuitClosedAsync(Circuit circuit, CancellationToken cancellationToken)
    {
        _logger.LogDebug("Blazor circuit closed CircuitId={CircuitId}", circuit.Id);
        return Task.CompletedTask;
    }

    public override Task OnConnectionDownAsync(Circuit circuit, CancellationToken cancellationToken)
    {
        _logger.LogInformation("Blazor circuit connection down CircuitId={CircuitId}", circuit.Id);
        return Task.CompletedTask;
    }
}
