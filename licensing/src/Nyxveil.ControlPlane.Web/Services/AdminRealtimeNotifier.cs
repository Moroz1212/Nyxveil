using Microsoft.AspNetCore.SignalR;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Web.Hubs;

namespace Nyxveil.ControlPlane.Web.Services;

/// <summary>Pushes lightweight refresh hints to admin SignalR subscribers. No-op-safe.</summary>
public sealed class AdminRealtimeNotifier : IAdminRealtimeNotifier
{
    public const string DashboardGroup = "dashboard";
    public const string RefreshMethod = "refresh";

    private readonly IHubContext<NodeStatusHub> _hub;
    private readonly ILogger<AdminRealtimeNotifier> _log;

    public AdminRealtimeNotifier(IHubContext<NodeStatusHub> hub, ILogger<AdminRealtimeNotifier> log)
    {
        _hub = hub;
        _log = log;
    }

    private async Task SafeSendAsync(CancellationToken cancellationToken)
    {
        try
        {
            await _hub.Clients.Group(DashboardGroup).SendAsync(RefreshMethod, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            _log.LogDebug(ex, "Admin realtime notify skipped");
        }
    }

    public Task NotifyDashboardAsync(CancellationToken cancellationToken = default) =>
        SafeSendAsync(cancellationToken);

    public Task NotifyNodeAsync(string nodeId, CancellationToken cancellationToken = default) =>
        SafeSendAsync(cancellationToken);

    public Task NotifyCommandAsync(Guid commandId, string nodeId, CancellationToken cancellationToken = default) =>
        SafeSendAsync(cancellationToken);
}
