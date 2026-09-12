using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.SelfUpdate;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class SelfUpdateTransactionStoreTests
{
    [Fact]
    public void Transaction_PersistedAndHistoryAppended()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-tx-" + Guid.NewGuid().ToString("N"));
        var store = new FileSelfUpdateTransactionStore(root);
        var tx = new SelfUpdateTransaction
        {
            TransactionId = Guid.NewGuid(),
            RequestedBy = "sa@test",
            CreatedAt = DateTime.UtcNow,
            CurrentVersion = "1.3.4",
            TargetVersion = "1.3.5",
            ReleaseTag = "control-plane-v1.3.5",
            PackageSha256 = new string('A', 64),
            Phase = SelfUpdatePhase.Downloading,
            Status = SelfUpdateStatus.InProgress,
            LastUpdatedAt = DateTime.UtcNow
        };
        store.Save(tx);
        var active = store.GetActive();
        Assert.NotNull(active);
        Assert.Equal(tx.TransactionId, active!.TransactionId);

        tx.Status = SelfUpdateStatus.Completed;
        tx.Phase = SelfUpdatePhase.Completed;
        tx.ResultCode = SelfUpdateResultCodes.UpdatedHealthy;
        store.AppendHistory(tx);
        Assert.Null(store.GetActive());
        Assert.Single(store.ListHistory(10));
    }

    [Fact]
    public void ConcurrentActive_DetectedByCallerPattern()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-tx2-" + Guid.NewGuid().ToString("N"));
        var store = new FileSelfUpdateTransactionStore(root);
        store.Save(new SelfUpdateTransaction
        {
            TransactionId = Guid.NewGuid(),
            Status = SelfUpdateStatus.InProgress,
            Phase = SelfUpdatePhase.Installing,
            CreatedAt = DateTime.UtcNow,
            LastUpdatedAt = DateTime.UtcNow
        });
        Assert.Equal(SelfUpdateStatus.InProgress, store.GetActive()!.Status);
    }

    [Fact]
    public void ControlPlaneSelfUpdate_IsSuperAdminCriticalOp()
    {
        Assert.True(CriticalOperationPolicy.RequiresSuperAdmin(CriticalOperation.ControlPlaneSelfUpdate));
        Assert.True(CriticalOperationPolicy.RequiresStepUp(CriticalOperation.ControlPlaneSelfUpdate));
    }
}
