using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

public static class NodeCommandCapabilities
{
    public static bool Supports(Node node, NodeCommandType type)
    {
        var required = type switch
        {
            NodeCommandType.UpdateNodeLatest => "node_update",
            NodeCommandType.RenewCertificate => "certificate_renew",
            NodeCommandType.RestartNyxveilService => "service_restart",
            NodeCommandType.RebootHost => "host_reboot",
            _ => null
        };
        return required is not null && node.SupportsNodeCommands && (node.ManagementCapabilities ?? "")
            .Split(',', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries)
            .Contains(required, StringComparer.OrdinalIgnoreCase);
    }
}
