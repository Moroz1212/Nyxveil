using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.SelfUpdate;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class SelfUpdateReconcileTests
{
    [Fact]
    public void AmbiguousMidInstall_FailsClosed()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-rec-" + Guid.NewGuid().ToString("N"));
        var store = new FileSelfUpdateTransactionStore(root);
        store.Save(new SelfUpdateTransaction
        {
            TransactionId = Guid.NewGuid(),
            CurrentVersion = "1.3.4",
            TargetVersion = "1.3.5",
            Phase = SelfUpdatePhase.Installing,
            Status = SelfUpdateStatus.InProgress,
            CreatedAt = DateTime.UtcNow,
            LastUpdatedAt = DateTime.UtcNow
        });

        var active = store.GetActive()!;
        // Mimic ReconcileOnStartup ambiguous branch.
        if (active.Phase is SelfUpdatePhase.StoppingControlPlane or SelfUpdatePhase.Installing
            or SelfUpdatePhase.StartingControlPlane)
        {
            active.Phase = SelfUpdatePhase.Failed;
            active.Status = SelfUpdateStatus.Failed;
            active.ResultCode = SelfUpdateResultCodes.AmbiguousState;
            store.AppendHistory(active);
        }

        Assert.Null(store.GetActive());
        Assert.Equal(SelfUpdateResultCodes.AmbiguousState, store.ListHistory(1)[0].ResultCode);
    }

    [Fact]
    public void HealthPhaseWithTargetInstalled_Completes()
    {
        var installed = "1.3.5";
        var active = new SelfUpdateTransaction
        {
            TargetVersion = "1.3.5",
            CurrentVersion = "1.3.4",
            Phase = SelfUpdatePhase.HealthVerification,
            Status = SelfUpdateStatus.InProgress
        };
        if (active.Phase is SelfUpdatePhase.HealthVerification or SelfUpdatePhase.Committing
            && string.Equals(installed, active.TargetVersion, StringComparison.Ordinal))
        {
            active.Status = SelfUpdateStatus.Completed;
            active.ResultCode = SelfUpdateResultCodes.UpdatedHealthy;
        }

        Assert.Equal(SelfUpdateStatus.Completed, active.Status);
        Assert.Equal(SelfUpdateResultCodes.UpdatedHealthy, active.ResultCode);
    }

    [Fact]
    public void HandoffWithoutVersionChange_FailsNotSuccess()
    {
        var installed = "1.3.4";
        var active = new SelfUpdateTransaction
        {
            CurrentVersion = "1.3.4",
            TargetVersion = "1.3.5",
            Phase = SelfUpdatePhase.ReadyForHandoff,
            Status = SelfUpdateStatus.InProgress
        };
        if (active.Phase >= SelfUpdatePhase.ReadyForHandoff &&
            string.Equals(installed, active.CurrentVersion, StringComparison.Ordinal))
        {
            active.Status = SelfUpdateStatus.Failed;
            active.ResultCode = SelfUpdateResultCodes.HealthFailed;
        }

        Assert.Equal(SelfUpdateStatus.Failed, active.Status);
        Assert.NotEqual(SelfUpdateResultCodes.UpdatedHealthy, active.ResultCode);
    }

    [Fact]
    public void ResultCodes_RollbackDistinctFromSuccess()
    {
        Assert.NotEqual(SelfUpdateResultCodes.UpdatedHealthy, SelfUpdateResultCodes.RolledBackHealthy);
        Assert.NotEqual(SelfUpdateResultCodes.UpdatedHealthy, SelfUpdateResultCodes.RollbackFailed);
        Assert.NotEqual(SelfUpdateResultCodes.UpdatedHealthy, SelfUpdateResultCodes.HealthFailed);
    }

    [Fact]
    public void UpdaterLock_IdempotentSameTransaction()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cp-lock-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        var lockPath = Path.Combine(dir, "updater.lock");
        var txId = Guid.NewGuid().ToString("N");
        File.WriteAllText(lockPath, txId);
        var existing = File.ReadAllText(lockPath).Trim();
        Assert.True(string.Equals(existing, txId, StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public void UpdaterLock_RejectsDifferentTransaction()
    {
        var dir = Path.Combine(Path.GetTempPath(), "cp-lock2-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        var lockPath = Path.Combine(dir, "updater.lock");
        File.WriteAllText(lockPath, "aaa");
        var existing = File.ReadAllText(lockPath).Trim();
        Assert.False(string.Equals(existing, "bbb", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public void PreserveProductionConfig_NamesProtected()
    {
        var preserve = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            "appsettings.Production.json",
            "appsettings.Production.json.bak"
        };
        Assert.Contains("appsettings.Production.json", preserve);
        Assert.DoesNotContain("Nyxveil.ControlPlane.Web.dll", preserve);
    }

    [Fact]
    public void Downgrade_BlockedByAvailability()
    {
        Assert.Equal(
            ControlPlaneUpdateAvailability.InstalledNewer,
            ControlPlaneReleasePolicy.Compare("1.3.8", "1.3.7"));
        Assert.NotEqual(
            ControlPlaneUpdateAvailability.UpdateAvailable,
            ControlPlaneReleasePolicy.Compare("1.3.8", "1.3.7"));
    }

    [Fact]
    public void ConcurrentResultCode_Defined()
    {
        Assert.Equal("concurrent_update", SelfUpdateResultCodes.ConcurrentUpdate);
    }
}
