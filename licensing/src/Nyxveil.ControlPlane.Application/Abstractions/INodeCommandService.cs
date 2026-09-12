using Nyxveil.ControlPlane.Application.Contracts.V1;
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

    /// <summary>
    /// Node-authenticated progress heartbeat. Refreshes the execution progress lease.
    /// </summary>
    Task ReportProgressAsync(
        Guid id,
        string nodeId,
        string? phase,
        string? message,
        CancellationToken cancellationToken = default);

    Task CompleteAsync(
        Guid id,
        string nodeId,
        bool success,
        string? resultCode,
        string? resultMessage,
        string? bootId = null,
        CancellationToken cancellationToken = default);

    Task ExpireStaleAsync(CancellationToken cancellationToken = default);

    /// <summary>
    /// SuperAdmin preview of whether an unknown UpdateNodeLatest outcome can be reconciled
    /// from current fresh node evidence (no mutation).
    /// </summary>
    Task<UnknownUpdateReconciliationPreview> GetUnknownUpdateReconciliationPreviewAsync(
        Guid commandId,
        string nodeId,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// SuperAdmin reconciliation of UpdateNodeLatest with expired_outcome_unknown /
    /// outcome_unknown / rollback_failed after verifying current effective version evidence.
    /// Restores admin_state_before via the shared restore path. Does not delete the command.
    /// </summary>
    Task<UnknownUpdateReconciliationResult> ReconcileUnknownUpdateAsync(
        UnknownUpdateReconciliationRequest request,
        CancellationToken cancellationToken = default);
}
