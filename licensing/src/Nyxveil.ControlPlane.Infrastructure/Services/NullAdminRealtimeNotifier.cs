namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>Default no-op notifier so Worker/API hosts without SignalR still resolve DI.</summary>
public sealed class NullAdminRealtimeNotifier : Application.Abstractions.IAdminRealtimeNotifier
{
    public Task NotifyDashboardAsync(CancellationToken cancellationToken = default) => Task.CompletedTask;
    public Task NotifyNodeAsync(string nodeId, CancellationToken cancellationToken = default) => Task.CompletedTask;
    public Task NotifyCommandAsync(Guid commandId, string nodeId, CancellationToken cancellationToken = default) =>
        Task.CompletedTask;
}
