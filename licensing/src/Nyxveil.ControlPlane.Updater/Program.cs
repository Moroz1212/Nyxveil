using System.Diagnostics;
using System.Text.Json;
using Nyxveil.ControlPlane.Application.SelfUpdate;

namespace Nyxveil.ControlPlane.Updater;

/// <summary>
/// External updater: must not run inside the Web process address space while overwriting binaries.
/// Invoked as: Nyxveil.ControlPlane.Updater.exe --handoff &lt;path&gt;
/// Preferentially launches scripts/self-update-apply.ps1 which reuses Deploy.psm1.
/// </summary>
public static class Program
{
    public static int Main(string[] args)
    {
        try
        {
            var handoff = ParseHandoff(args);
            if (string.IsNullOrWhiteSpace(handoff) || !File.Exists(handoff))
            {
                Console.Error.WriteLine("usage: Nyxveil.ControlPlane.Updater --handoff <handoff.json>");
                return 2;
            }

            using var doc = JsonDocument.Parse(File.ReadAllText(handoff));
            var root = doc.RootElement;
            var staging = root.GetProperty("stagingPath").GetString() ?? "";
            var installDir = root.GetProperty("installDir").GetString() ?? "";
            var serviceName = root.GetProperty("serviceName").GetString() ?? "NyxveilControlPlane";
            var backupPath = root.TryGetProperty("backupPath", out var b) ? b.GetString() ?? "" : "";
            var targetVersion = root.GetProperty("targetVersion").GetString() ?? "";
            var currentVersion = root.GetProperty("currentVersion").GetString() ?? "";
            var txId = root.GetProperty("transactionId").GetString() ?? "";

            if (string.IsNullOrWhiteSpace(staging) || string.IsNullOrWhiteSpace(installDir))
            {
                Console.Error.WriteLine("invalid handoff: stagingPath/installDir required");
                return 3;
            }

            // Idempotent lock file
            var lockPath = Path.Combine(Path.GetDirectoryName(handoff)!, "updater.lock");
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
                {
                    var code = RunPowerShell(script, handoff);
                    return code;
                }

                // Minimal built-in path when scripts are unavailable (lab/dev).
                return RunBuiltin(staging, installDir, serviceName, backupPath, currentVersion, targetVersion, handoff);
            }
            finally
            {
                try { if (File.Exists(lockPath)) File.Delete(lockPath); } catch { /* ignore */ }
            }
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
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
        string handoffPath)
    {
        Directory.CreateDirectory(backupPath);
        // Preserve production config files.
        var preserve = new HashSet<string>(StringComparer.OrdinalIgnoreCase)
        {
            "appsettings.Production.json",
            "appsettings.Production.json.bak"
        };

        StopService(serviceName);
        BackupDirectory(installDir, Path.Combine(backupPath, "install"), preserve);
        CopyPublish(staging, installDir, preserve);

        // Ensure VERSION matches target after copy if present in staging root parent.
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
            Console.Error.WriteLine("target version mismatch; rolling back");
            RestoreDirectory(Path.Combine(backupPath, "install"), installDir);
            StartService(serviceName);
            WriteResult(handoffPath, SelfUpdateResultCodes.RolledBackHealthy, currentVersion);
            return 10;
        }

        WriteResult(handoffPath, SelfUpdateResultCodes.UpdatedHealthy, targetVersion);
        return 0;
    }

    private static void WriteResult(string handoffPath, string code, string version)
    {
        var dir = Path.GetDirectoryName(handoffPath)!;
        File.WriteAllText(Path.Combine(dir, "result.json"),
            $"{{\"resultCode\":\"{code}\",\"version\":\"{version}\",\"at\":\"{DateTime.UtcNow:O}\"}}");
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

    private static void BackupDirectory(string source, string dest, HashSet<string> preserveNames)
    {
        _ = preserveNames;
        if (Directory.Exists(dest)) Directory.Delete(dest, recursive: true);
        CopyRecursive(source, dest, skip: null);
    }

    private static void RestoreDirectory(string backup, string installDir)
    {
        if (!Directory.Exists(backup)) return;
        CopyRecursive(backup, installDir, skip: null);
    }

    private static void CopyPublish(string staging, string installDir, HashSet<string> preserve)
    {
        foreach (var file in Directory.GetFiles(staging, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(staging, file);
            var name = Path.GetFileName(file);
            if (preserve.Contains(name) && File.Exists(Path.Combine(installDir, rel)))
                continue;
            // Never overwrite config/ from package over production config.
            if (rel.StartsWith("config" + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
                || rel.StartsWith("config/", StringComparison.OrdinalIgnoreCase))
                continue;
            var dest = Path.Combine(installDir, rel);
            Directory.CreateDirectory(Path.GetDirectoryName(dest)!);
            File.Copy(file, dest, overwrite: true);
        }
    }

    private static void CopyRecursive(string source, string dest, HashSet<string>? skip)
    {
        Directory.CreateDirectory(dest);
        foreach (var dir in Directory.GetDirectories(source, "*", SearchOption.AllDirectories))
        {
            var rel = Path.GetRelativePath(source, dir);
            Directory.CreateDirectory(Path.Combine(dest, rel));
        }

        foreach (var file in Directory.GetFiles(source, "*", SearchOption.AllDirectories))
        {
            var name = Path.GetFileName(file);
            if (skip is not null && skip.Contains(name)) continue;
            var rel = Path.GetRelativePath(source, file);
            var target = Path.Combine(dest, rel);
            Directory.CreateDirectory(Path.GetDirectoryName(target)!);
            File.Copy(file, target, overwrite: true);
        }
    }
}
