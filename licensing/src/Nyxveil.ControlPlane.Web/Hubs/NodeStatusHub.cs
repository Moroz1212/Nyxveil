using Microsoft.AspNetCore.SignalR;

namespace Nyxveil.ControlPlane.Web.Hubs;

/// <summary>Optional realtime hub for admin dashboard node status updates.</summary>
public sealed class NodeStatusHub : Hub
{
    public Task SubscribeAsync() => Groups.AddToGroupAsync(Context.ConnectionId, "dashboard");
}
