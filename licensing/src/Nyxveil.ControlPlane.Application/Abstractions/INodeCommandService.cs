using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface INodeCommandService
{
    Task<NodeCommand> EnqueueAsync(
        string nodeId,
        NodeCommandType type,
        string actor,
        IEnumerable<string> roles,
        CancellationToken cancellationToken = default);

    Task<IReadOnlyList<NodeCommand>> ListForNodeAsync(
        string nodeId,
        int take = 50,
        CancellationToken cancellationToken = default);

    Task<IReadOnlyList<NodeCommand>> ListRecentAsync(
        int take = 100,
        CancellationToken cancellationToken = default);

    /// <summary>Claims one Pending non-expired command for the node, or null if none.</summary>
    Task<NodeCommand?> ClaimNextAsync(string nodeId, CancellationToken cancellationToken = default);

    Task MarkStartedAsync(Guid id, string nodeId, CancellationToken cancellationToken = default);

    Task CompleteAsync(
        Guid id,
        string nodeId,
        bool success,
        string? resultCode,
        string? resultMessage,
        string? bootId = null,
        CancellationToken cancellationToken = default);

    Task ExpireStaleAsync(CancellationToken cancellationToken = default);
}
