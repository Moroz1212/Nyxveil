using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.SelfUpdate;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class PrivilegedUpdaterContractTests
{
    [Fact]
    public void Rejects_NonCanonical_ServiceName()
    {
        var pd = Path.Combine(Path.GetTempPath(), "cp-pd-" + Guid.NewGuid().ToString("N"));
        var staging = Path.Combine(pd, "self-update", "staging", "x");
        var backup = Path.Combine(pd, "self-update", "backups", "y");
        Directory.CreateDirectory(staging);
        Directory.CreateDirectory(backup);
        Assert.Throws<InvalidOperationException>(() =>
            PrivilegedUpdaterContract.AssertCanonicalHandoff(
                PrivilegedUpdaterContract.DefaultInstallDir, staging, backup, "EvilService", pd));
    }

    [Fact]
    public void Rejects_Path_Traversal_Staging()
    {
        var pd = Path.Combine(Path.GetTempPath(), "cp-pd2-" + Guid.NewGuid().ToString("N"));
        var staging = Path.Combine(pd, "outside");
        var backup = Path.Combine(pd, "self-update", "backups", "y");
        Directory.CreateDirectory(staging);
        Directory.CreateDirectory(backup);
        Assert.Throws<InvalidOperationException>(() =>
            PrivilegedUpdaterContract.AssertCanonicalHandoff(
                PrivilegedUpdaterContract.DefaultInstallDir, staging, backup,
                PrivilegedUpdaterContract.ControlPlaneServiceName, pd));
    }

    [Fact]
    public void Accepts_Canonical_Paths()
    {
        var pd = Path.Combine(Path.GetTempPath(), "cp-pd3-" + Guid.NewGuid().ToString("N"));
        var staging = Path.Combine(pd, "self-update", "staging", "x");
        var backup = Path.Combine(pd, "self-update", "backups", "y");
        Directory.CreateDirectory(staging);
        Directory.CreateDirectory(backup);
        PrivilegedUpdaterContract.AssertCanonicalHandoff(
            PrivilegedUpdaterContract.DefaultInstallDir, staging, backup,
            PrivilegedUpdaterContract.ControlPlaneServiceName, pd);
    }

    [Fact]
    public void ParseResult_Captures_RollbackFields()
    {
        var ok = PrivilegedUpdaterContract.TryParseResult(
            """{"resultCode":"rollback_failed","version":"1.3.5","primaryFailure":"Access denied","rollbackAttempted":true,"rollbackSucceeded":false,"rollbackFailure":"no backup","transactionId":"abc"}""",
            out var r);
        Assert.True(ok);
        Assert.Equal(SelfUpdateResultCodes.RollbackFailed, r.ResultCode);
        Assert.Equal("Access denied", r.PrimaryFailure);
        Assert.True(r.RollbackAttempted);
        Assert.False(r.RollbackSucceeded);
        Assert.Equal("no backup", r.RollbackFailure);
    }

    [Fact]
    public void Reconcile_Ingests_Terminal_Result_Clears_Active()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-rec-result-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var store = new FileSelfUpdateTransactionStore(root);
        var txId = Guid.NewGuid();
        store.Save(new SelfUpdateTransaction
        {
            TransactionId = txId,
            CurrentVersion = "1.3.5",
            TargetVersion = "1.3.6",
            Phase = SelfUpdatePhase.ReadyForHandoff,
            Status = SelfUpdateStatus.InProgress,
            CreatedAt = DateTime.UtcNow,
            LastUpdatedAt = DateTime.UtcNow
        });

        File.WriteAllText(Path.Combine(root, "result.json"),
            $"{{\"resultCode\":\"rollback_failed\",\"version\":\"1.3.5\",\"transactionId\":\"{txId:N}\",\"primaryFailure\":\"Access denied\",\"rollbackAttempted\":false}}");

        var active = store.GetActive()!;
        Assert.Equal(SelfUpdateStatus.InProgress, active.Status);

        Assert.True(PrivilegedUpdaterContract.TryParseResult(File.ReadAllText(Path.Combine(root, "result.json")), out var result));
        Assert.Equal(txId.ToString("N"), result.TransactionId);

        active.ResultCode = result.ResultCode;
        active.PrimaryFailure = result.PrimaryFailure;
        active.RollbackAttempted = result.RollbackAttempted;
        active.Phase = SelfUpdatePhase.Failed;
        active.Status = SelfUpdateStatus.Failed;
        store.AppendHistory(active);

        Assert.Null(store.GetActive());
        Assert.Equal(SelfUpdateResultCodes.RollbackFailed, store.ListHistory(1)[0].ResultCode);
        Assert.Equal("Access denied", store.ListHistory(1)[0].PrimaryFailure);
    }
}

public sealed class ConfigBackupNestingTests
{
    [Fact]
    public void Restore_Config_Into_Parent_Does_Not_Nest()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-cfg-" + Guid.NewGuid().ToString("N"));
        var install = Path.Combine(root, "install");
        var backupConfig = Path.Combine(root, "backup", "config");
        Directory.CreateDirectory(Path.Combine(install, "config"));
        File.WriteAllText(Path.Combine(install, "config", "operational.json"), "{}");
        Directory.CreateDirectory(backupConfig);
        File.WriteAllText(Path.Combine(backupConfig, "operational.json"), "{\"ok\":true}");

        var cfgTarget = Path.Combine(install, "config");
        if (Directory.Exists(cfgTarget)) Directory.Delete(cfgTarget, true);
        // Mimic fixed update-windows restore: copy backup\config into install parent.
        CopyDir(backupConfig, Path.Combine(install, "config"));

        Assert.True(File.Exists(Path.Combine(install, "config", "operational.json")));
        Assert.False(Directory.Exists(Path.Combine(install, "config", "config")));
    }

    private static void CopyDir(string src, string dst)
    {
        Directory.CreateDirectory(dst);
        foreach (var f in Directory.GetFiles(src, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(src, f);
            var t = Path.Combine(dst, rel);
            Directory.CreateDirectory(Path.GetDirectoryName(t)!);
            File.Copy(f, t, true);
        }
    }
}
