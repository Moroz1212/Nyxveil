using Microsoft.AspNetCore.Components;
using Microsoft.AspNetCore.SignalR.Client;

namespace Nyxveil.ControlPlane.Web.Services;

/// <summary>
/// Optional SignalR client for admin pages. Failures are swallowed; callers keep polling.
/// </summary>
public sealed class AdminRealtimeClient : IAsyncDisposable
{
    private HubConnection? _connection;
    private Func<Task>? _onRefresh;

    public bool IsConnected => _connection?.State == HubConnectionState.Connected;

    public static async Task<AdminRealtimeClient> ConnectAsync(
        NavigationManager navigation,
        Func<Task> onRefresh,
        CancellationToken cancellationToken = default)
    {
        var client = new AdminRealtimeClient { _onRefresh = onRefresh };
        try
        {
            var url = navigation.ToAbsoluteUri("/hubs/node-status");
            client._connection = new HubConnectionBuilder()
                .WithUrl(url)
                .WithAutomaticReconnect()
                .Build();

            client._connection.On("refresh", async () =>
            {
                if (client._onRefresh is not null)
                    await client._onRefresh().ConfigureAwait(false);
            });

            await client._connection.StartAsync(cancellationToken).ConfigureAwait(false);
            await client._connection.InvokeAsync("SubscribeAsync", cancellationToken).ConfigureAwait(false);
        }
        catch
        {
            if (client._connection is not null)
            {
                try { await client._connection.DisposeAsync().ConfigureAwait(false); } catch { /* ignore */ }
                client._connection = null;
            }
        }

        return client;
    }

    public async ValueTask DisposeAsync()
    {
        if (_connection is null) return;
        try { await _connection.DisposeAsync().ConfigureAwait(false); }
        catch { /* ignore */ }
        _connection = null;
    }
}
