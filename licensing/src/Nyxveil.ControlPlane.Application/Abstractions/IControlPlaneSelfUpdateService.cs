using Nyxveil.ControlPlane.Application.SelfUpdate;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IControlPlaneReleaseService
{
    Task<ControlPlaneReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default);
    Task<ControlPlaneReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default);
}

public interface IControlPlaneSelfUpdateService
{
    Task<ControlPlaneUpdateStatusDto> GetStatusAsync(CancellationToken cancellationToken = default);
    Task<ControlPlaneUpdatePreflightDto> EvaluatePreflightAsync(CancellationToken cancellationToken = default);
    Task<SelfUpdateTransaction> StartUpdateAsync(string actor, IEnumerable<string> roles, CancellationToken cancellationToken = default);
    Task<SelfUpdateTransaction?> GetActiveTransactionAsync(CancellationToken cancellationToken = default);
    Task<IReadOnlyList<SelfUpdateTransaction>> GetHistoryAsync(int take = 20, CancellationToken cancellationToken = default);
    /// <summary>Called on CP startup to reconcile incomplete transactions fail-closed.</summary>
    Task ReconcileOnStartupAsync(CancellationToken cancellationToken = default);
}

public interface ISelfUpdateTransactionStore
{
    SelfUpdateTransaction? GetActive();
    IReadOnlyList<SelfUpdateTransaction> ListHistory(int take = 50);
    void Save(SelfUpdateTransaction tx);
    void AppendHistory(SelfUpdateTransaction tx);
}
