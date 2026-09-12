using System.Diagnostics;
using System.Text.Json;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application.SelfUpdate;

namespace Nyxveil.ControlPlane.Updater;

/// <summary>
/// Privileged updater host. Production: Windows service LocalSystem polling request.json.
/// Lab: --handoff &lt;path&gt; for a one-shot apply.
/// </summary>
public static class Program
{
    public const string ServiceName = PrivilegedUpdaterContract.UpdaterServiceName;

    public static async Task<int> Main(string[] args)
    {
        if (args.Any(a => string.Equals(a, "--service", StringComparison.OrdinalIgnoreCase)))
        {
            var builder = Host.CreateApplicationBuilder(args);
            builder.Services.AddWindowsService(o => o.ServiceName = ServiceName);
            builder.Services.AddHostedService<UpdaterWorker>();
            await builder.Build().RunAsync().ConfigureAwait(false);
            return 0;
        }

        try
        {
            var handoff = ParseHandoff(args);
            if (string.IsNullOrWhiteSpace(handoff) || !File.Exists(handoff))
            {
                Console.Error.WriteLine("usage: Nyxveil.ControlPlane.Updater --service | --handoff <handoff.json>");
                return 2;
            }

            return ApplyOnce(handoff);
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }

    internal static int ApplyOnce(string handoffPath)
    {
        using var doc = JsonDocument.Parse(File.ReadAllText(handoffPath));
        var root = doc.RootElement;
        var staging = root.GetProperty("stagingPath").GetString() ?? "";
        var installDir = root.GetProperty("installDir").GetString() ?? "";
        var serviceName = root.GetProperty("serviceName").GetString() ?? PrivilegedUpdaterContract.ControlPlaneServiceName;
        var backupPath = root.TryGetProperty("backupPath", out var b) ? b.GetString() ?? "" : "";
        var targetVersion = root.GetProperty("targetVersion").GetString() ?? "";
        var currentVersion = root.GetProperty("currentVersion").GetString() ?? "";
        var txId = root.GetProperty("transactionId").GetString() ?? "";

        PrivilegedUpdaterContract.AssertCanonicalHandoff(installDir, staging, backupPath, serviceName);

        var lockPath = Path.Combine(Path.GetDirectoryName(handoffPath)!, "updater.lock");
        if (File.Exists(lockPath))
        {
            var existing = File.ReadAllText(lockPath).Trim();
            if (string.Equals(existing, txId, StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine("idempotent: updater already owns this transaction");
                return 0;
            }

            Console.Error.WriteLine("another updater lock is active");
            return 4;
        }

        File.WriteAllText(lockPath, txId);
        try
        {
            var script = FindApplyScript(installDir);
            if (script is not null)
                return RunPowerShell(script, handoffPath);

            return RunBuiltin(staging, installDir, serviceName, backupPath, currentVersion, targetVersion, handoffPath, txId);
        }
        finally
        {
            try { if (File.Exists(lockPath)) File.Delete(lockPath); } catch { /* ignore */ }
        }
    }

    private static string? ParseHandoff(string[] args)
    {
        for (var i = 0; i < args.Length - 1; i++)
        {
            if (args[i] is "--handoff" or "-h")
                return args[i + 1];
        }

        return null;
    }

    private static string? FindApplyScript(string installDir)
    {
        var candidates = new[]
        {
            Path.Combine(installDir, "scripts", "self-update-apply.ps1"),
            Path.Combine(AppContext.BaseDirectory, "scripts", "self-update-apply.ps1"),
            Path.Combine(AppContext.BaseDirectory, "..", "scripts", "self-update-apply.ps1")
        };
        return candidates.Select(Path.GetFullPath).FirstOrDefault(File.Exists);
    }

    private static int RunPowerShell(string script, string handoff)
    {
        var psi = new ProcessStartInfo
        {
            FileName = OperatingSystem.IsWindows() ? "powershell.exe" : "pwsh",
            Arguments = $"-NoProfile -ExecutionPolicy Bypass -File \"{script}\" -HandoffPath \"{handoff}\"",
            UseShellExecute = false
        };
        using var p = Process.Start(psi) ?? throw new InvalidOperationException("failed to start powershell");
        p.WaitForExit();
        return p.ExitCode;
    }

    private static int RunBuiltin(
        string staging,
        string installDir,
        string serviceName,
        string backupPath,
        string currentVersion,
        string targetVersion,
        string handoffPath,
        string txId)
    {
        Directory.CreateDirectory(backupPath);
        var preserve = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            "appsettings.Production.json",
            "appsettings.Production.json.bak"
        };

        var installBackup = Path.Combine(backupPath, "install");
        string? primaryFailure = null;
        var mutable = false;
        try
        {
            BackupDirectory(installDir, installBackup);
            mutable = true;
            StopService(serviceName);
            CopyPublish(staging, installDir, preserve);

            var versionSrc = Path.Combine(Directory.GetParent(staging)?.FullName ?? staging, "VERSION");
            if (!File.Exists(versionSrc))
                versionSrc = Path.Combine(staging, "VERSION");
            if (File.Exists(versionSrc))
                File.Copy(versionSrc, Path.Combine(installDir, "VERSION"), overwrite: true);

            StartService(serviceName);
            Thread.Sleep(TimeSpan.FromSeconds(5));

            var installed = File.Exists(Path.Combine(installDir, "VERSION"))
                ? File.ReadAllText(Path.Combine(installDir, "VERSION")).Trim()
                : "";
            if (!string.Equals(installed, targetVersion, StringComparison.Ordinal))
            {
                primaryFailure = $"version mismatch installed={installed} target={targetVersion}";
                return Rollback(installBackup, installDir, serviceName, handoffPath, currentVersion, primaryFailure, txId);
            }

            WriteResult(handoffPath, SelfUpdateResultCodes.UpdatedHealthy, targetVersion, txId);
            return 0;
        }
        catch (Exception ex)
        {
            primaryFailure = ex.Message;
            if (!mutable)
            {
                WriteResult(handoffPath, SelfUpdateResultCodes.RollbackFailed, currentVersion, txId,
                    primaryFailure, rollbackAttempted: false, rollbackSucceeded: false,
                    rollbackFailure: "mutable phase not started");
                return 1;
            }

            return Rollback(installBackup, installDir, serviceName, handoffPath, currentVersion, primaryFailure, txId);
        }
    }

    private static int Rollback(
        string installBackup,
        string installDir,
        string serviceName,
        string handoffPath,
        string currentVersion,
        string primaryFailure,
        string txId)
    {
        try
        {
            StopService(serviceName);
            if (Directory.Exists(installBackup))
                RestoreDirectory(installBackup, installDir);
            StartService(serviceName);
            WriteResult(handoffPath, SelfUpdateResultCodes.RolledBackHealthy, currentVersion, txId,
                primaryFailure, rollbackAttempted: true, rollbackSucceeded: true);
            return 11;
        }
        catch (Exception ex)
        {
            WriteResult(handoffPath, SelfUpdateResultCodes.RollbackFailed, currentVersion, txId,
                primaryFailure, rollbackAttempted: true, rollbackSucceeded: false, rollbackFailure: ex.Message);
            return 12;
        }
    }

    private static void WriteResult(
        string handoffPath,
        string code,
        string version,
        string txId,
        string? primaryFailure = null,
        bool rollbackAttempted = false,
        bool? rollbackSucceeded = null,
        string? rollbackFailure = null)
    {
        var dir = Path.GetDirectoryName(handoffPath)!;
        var payload = new Dictionary<string, object?>
        {
            ["resultCode"] = code,
            ["version"] = version,
            ["transactionId"] = txId,
            ["at"] = DateTime.UtcNow.ToString("O"),
            ["primaryFailure"] = primaryFailure,
            ["rollbackAttempted"] = rollbackAttempted,
            ["rollbackSucceeded"] = rollbackSucceeded,
            ["rollbackFailure"] = rollbackFailure
        };
        File.WriteAllText(Path.Combine(dir, PrivilegedUpdaterContract.ResultFileName),
            JsonSerializer.Serialize(payload));
    }

    private static void StopService(string name)
    {
        if (!OperatingSystem.IsWindows()) return;
        Run("sc.exe", $"stop {name}");
        Thread.Sleep(TimeSpan.FromSeconds(3));
    }

    private static void StartService(string name)
    {
        if (!OperatingSystem.IsWindows()) return;
        Run("sc.exe", $"start {name}");
    }

    private static void Run(string file, string args)
    {
        using var p = Process.Start(new ProcessStartInfo
        {
            FileName = file,
            Arguments = args,
            UseShellExecute = false,
            CreateNoWindow = true
        });
        p?.WaitForExit(120_000);
    }

    private static void BackupDirectory(string source, string dest)
    {
        if (Directory.Exists(dest)) Directory.Delete(dest, recursive: true);
        CopyRecursive(source, dest);
    }

    private static void RestoreDirectory(string backup, string installDir)
    {
        if (!Directory.Exists(backup)) return;
        foreach (var child in Directory.GetFileSystemEntries(installDir))
        {
            try
            {
                if (Directory.Exists(child)) Directory.Delete(child, true);
                else File.Delete(child);
            }
            catch { /* best effort */ }
        }

        CopyRecursive(backup, installDir);
    }

    private static void CopyPublish(string staging, string installDir, HashSet<string> preserve)
    {
        foreach (var file in Directory.GetFiles(staging, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(staging, file);
            var name = Path.GetFileName(file);
            if (string.Equals(name, "appsettings.Development.json", StringComparison.OrdinalIgnoreCase))
                continue;
            if (preserve.Contains(name) && File.Exists(Path.Combine(installDir, rel)))
                continue;
            if (rel.StartsWith("config" + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
                || rel.StartsWith("config/", StringComparison.OrdinalIgnoreCase))
                continue;
            var dest = Path.Combine(installDir, rel);
            Directory.CreateDirectory(Path.GetDirectoryName(dest)!);
            try
            {
                File.Copy(file, dest, overwrite: true);
            }
            catch (IOException) when (rel.StartsWith("updater" + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
                                      || rel.StartsWith("updater/", StringComparison.OrdinalIgnoreCase))
            {
                // Running updater cannot overwrite its own image; Web payload still updates.
            }
            catch (UnauthorizedAccessException) when (rel.StartsWith("updater" + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
                                                      || rel.StartsWith("updater/", StringComparison.OrdinalIgnoreCase))
            {
            }
        }
    }

    private static void CopyRecursive(string source, string dest)
    {
        Directory.CreateDirectory(dest);
        foreach (var dir in Directory.GetDirectories(source, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(source, dir);
            Directory.CreateDirectory(Path.Combine(dest, rel));
        }

        foreach (var file in Directory.GetFiles(source, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(source, file);
            var target = Path.Combine(dest, rel);
            Directory.CreateDirectory(Path.GetDirectoryName(target)!);
            File.Copy(file, target, overwrite: true);
        }
    }
}
