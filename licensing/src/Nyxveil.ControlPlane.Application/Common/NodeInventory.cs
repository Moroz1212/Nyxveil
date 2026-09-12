using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>
/// Operator inventory filters: Deleted nodes stay in DB for audit/integrity but are excluded from UI/ops queries.
/// </summary>
public static class NodeInventory
{
    public static bool IsOperatorVisible(NodeLifecycleState state) =>
        state != NodeLifecycleState.Deleted;

    public static bool IsOperatorVisible(Node node) =>
        node is not null && IsOperatorVisible(node.LifecycleState);

    public static IQueryable<Node> OperatorVisible(this IQueryable<Node> query) =>
        query.Where(n => n.LifecycleState != NodeLifecycleState.Deleted);

    public static IEnumerable<Node> OperatorVisible(this IEnumerable<Node> nodes) =>
        nodes.Where(IsOperatorVisible);
}
