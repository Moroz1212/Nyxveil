using System.Diagnostics;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class ControlPlaneSelfUpdateService : IControlPlaneSelfUpdateService
{
    public const string ServiceName = "NyxveilControlPlane";
    private static readonly long MinFreeDiskBytes = 512L * 1024 * 1024;

    private readonly IControlPlaneReleaseService _releases;
    private readonly ISelfUpdateTransactionStore _store;
    private readonly ICriticalOperationAuthorizer _critical;
    private readonly IAuditService _audit;
    private readonly IClock _clock;
    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly IConfiguration _configuration;
    private readonly ILogger<ControlPlaneSelfUpdateService> _log;
    private readonly object _startGate = new();

    public ControlPlaneSelfUpdateService(
        IControlPlaneReleaseService releases,
        ISelfUpdateTransactionStore store,
        ICriticalOperationAuthorizer critical,
        IAuditService audit,
        IClock clock,
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        IConfiguration configuration,
        ILogger<ControlPlaneSelfUpdateService> log)
    {
        _releases = releases;
        _store = store;
        _critical = critical;
        _audit = audit;
        _clock = clock;
        _dbFactory = dbFactory;
        _configuration = configuration;
        _log = log;
    }

    public async Task<ControlPlaneUpdateStatusDto> GetStatusAsync(CancellationToken cancellationToken = default)
    {
        var installed = ReadInstalledVersion();
        var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
        var availability = ControlPlaneReleasePolicy.Compare(installed, release.LatestVersion);
        var active = _store.GetActive();
        var verified = !string.IsNullOrWhiteSpace(release.ExpectedSha256) ? "verified"
            : release.SourceStatus is "ok" or "cached" ? "unknown" : "failed";

        // Do not allow update when remote release identity cannot be verified (missing assets or failed discovery).
        var can = availability == ControlPlaneUpdateAvailability.UpdateAvailable
                  && release.SourceStatus is "ok" or "cached"
                  && !string.IsNullOrWhiteSpace(release.PackageUrl)
                  && !string.IsNullOrWhiteSpace(release.ChecksumUrl)
                  && !string.IsNullOrWhiteSpace(release.ExpectedSha256)
                  && active is null or { Status: not SelfUpdateStatus.InProgress };

        string? block = null;
        if (availability == ControlPlaneUpdateAvailability.Current)
            block = "Актуальная версия";
        else if (availability == ControlPlaneUpdateAvailability.InstalledNewer)
            block = ControlPlaneReleasePolicy.StatusLabelRu(availability);
        else if (release.SourceStatus is not ("ok" or "cached"))
            block = "Удалённый релиз не проверен";
        else if (string.IsNullOrWhiteSpace(release.ExpectedSha256))
            block = "Checksum релиза не подтверждён";
        else if (active?.Status == SelfUpdateStatus.InProgress)
            block = "Обновление уже выполняется";

        return new ControlPlaneUpdateStatusDto
        {
            InstalledVersion = installed,
            LatestStableVersion = release.LatestVersion,
            ReleaseTag = release.ReleaseTag,
            PublishedAt = release.PublishedAt,
            Availability = availability,
            StatusLabelRu = ControlPlaneReleasePolicy.StatusLabelRu(availability),
            PackageVerification = verified,
            PackageSha256 = release.ExpectedSha256,
            CanUpdate = can && block is null,
            BlockReason = block,
            ActiveTransaction = active,
            History = _store.ListHistory(20)
        };
    }

    public async Task<ControlPlaneUpdatePreflightDto> EvaluatePreflightAsync(CancellationToken cancellationToken = default)
    {
        var status = await GetStatusAsync(cancellationToken).ConfigureAwait(false);
        var release = await _releases.RefreshAsync(cancellationToken).ConfigureAwait(false);
        var blockers = new List<string>();
        var warnings = new List<string>();

        var installed = status.InstalledVersion;
        var target = release.LatestVersion ?? "";
        var availability = ControlPlaneReleasePolicy.Compare(installed, target);
        if (availability != ControlPlaneUpdateAvailability.UpdateAvailable)
            blockers.Add(ControlPlaneReleasePolicy.StatusLabelRu(availability));
        if (release.SourceStatus is not ("ok" or "cached"))
            blockers.Add("релиз недоступен или не проверен");
        if (string.IsNullOrWhiteSpace(release.PackageUrl) || string.IsNullOrWhiteSpace(release.ChecksumUrl))
            blockers.Add("отсутствует package или checksum asset");
        if (string.IsNullOrWhiteSpace(release.ExpectedSha256))
            blockers.Add("checksum релиза не подтверждён");

        var active = _store.GetActive();
        if (active?.Status == SelfUpdateStatus.InProgress)
            blockers.Add("уже есть активная транзакция обновления");

        var dbOk = false;
        try
        {
            await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
            dbOk = await db.Database.CanConnectAsync(cancellationToken).ConfigureAwait(false);
        }
        catch { dbOk = false; }
        if (!dbOk) blockers.Add("база данных недоступна");

        var installDir = ResolveInstallDir();
        var drive = string.IsNullOrWhiteSpace(installDir) ? Path.GetPathRoot(Environment.SystemDirectory)! : Path.GetPathRoot(installDir)!;
        long free = 0;
        try
        {
            var di = new DriveInfo(drive);
            free = di.AvailableFreeSpace;
            if (free < MinFreeDiskBytes)
                blockers.Add("недостаточно места на диске");
        }
        catch
        {
            warnings.Add("не удалось определить свободное место на диске");
        }

        var serviceState = ProbeServiceState();
        if (OperatingSystem.IsWindows() && serviceState is "Stopped" or "StopPending")
            warnings.Add("служба сейчас не Running");

        return new ControlPlaneUpdatePreflightDto
        {
            Allowed = blockers.Count == 0,
            InstalledVersion = installed,
            TargetVersion = target,
            ReleaseTag = release.ReleaseTag ?? "",
            ReleaseDate = release.PublishedAt,
            PackageSha256 = release.ExpectedSha256,
            SchemaVersion = "5",
            ExpectedSchema = "5",
            ServiceState = serviceState,
            InstallDirectory = installDir,
            BackupAvailable = Directory.Exists(Path.Combine(ServiceCollectionExtensions.GetProgramDataRoot(), "backups")),
            FreeDiskBytes = free,
            HttpsConfigured = true,
            DatabaseConnected = dbOk,
            HealthStatus = dbOk ? "ready_probe_deferred" : "db_fail",
            BlockingReasons = blockers,
            Warnings = warnings
        };
    }

    public async Task<SelfUpdateTransaction> StartUpdateAsync(
        string actor, IEnumerable<string> roles, CancellationToken cancellationToken = default)
    {
        _critical.AssertAllowed(CriticalOperation.ControlPlaneSelfUpdate, roles);

        lock (_startGate)
        {
            var existing = _store.GetActive();
            if (existing?.Status == SelfUpdateStatus.InProgress)
                throw new ConflictException(SelfUpdateResultCodes.ConcurrentUpdate);
        }

        var preflight = await EvaluatePreflightAsync(cancellationToken).ConfigureAwait(false);
        if (!preflight.Allowed)
            throw new ValidationException(SelfUpdateResultCodes.PreflightFailed + ": " +
                                          string.Join("; ", preflight.BlockingReasons));

        var release = await _releases.RefreshAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var root = Path.Combine(ServiceCollectionExtensions.GetProgramDataRoot(), "self-update");
        var staging = Path.Combine(root, "staging", Guid.NewGuid().ToString("N"));
        var backup = Path.Combine(root, "backups", now.ToString("yyyyMMddHHmmss"));
        Directory.CreateDirectory(staging);

        var tx = new SelfUpdateTransaction
        {
            TransactionId = Guid.NewGuid(),
            RequestedBy = actor,
            CreatedAt = now,
            StartedAt = now,
            CurrentVersion = preflight.InstalledVersion,
            TargetVersion = release.LatestVersion!,
            ReleaseTag = release.ReleaseTag!,
            PackageUrl = release.PackageUrl,
            ChecksumUrl = release.ChecksumUrl,
            Phase = SelfUpdatePhase.Downloading,
            Status = SelfUpdateStatus.InProgress,
            ProgressMessage = "Загрузка пакета",
            LastUpdatedAt = now,
            StagingPath = staging,
            BackupPath = backup
        };
        Mark(tx, SelfUpdatePhase.Downloading, "Загрузка release package", ok: true);
        _store.Save(tx);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = actor,
            Action = "controlplane.update.requested",
            EntityType = "ControlPlane",
            EntityId = tx.TransactionId.ToString("N"),
            Detail = $"from={tx.CurrentVersion};to={tx.TargetVersion};tag={tx.ReleaseTag}"
        }, cancellationToken).ConfigureAwait(false);

        try
        {
            var client = new HttpClient { Timeout = TimeSpan.FromMinutes(10) };
            client.DefaultRequestHeaders.UserAgent.ParseAdd("Nyxveil-ControlPlane-SelfUpdate");

            var zipPath = Path.Combine(staging, ControlPlaneReleasePolicy.ExpectedPackageName(tx.TargetVersion));
            var sumPath = Path.Combine(staging, ControlPlaneReleasePolicy.ExpectedChecksumName(tx.TargetVersion));

            await DownloadAsync(client, release.ChecksumUrl!, sumPath, cancellationToken).ConfigureAwait(false);
            await DownloadAsync(client, release.PackageUrl!, zipPath, cancellationToken).ConfigureAwait(false);

            tx.Phase = SelfUpdatePhase.VerifyingPackage;
            Mark(tx, SelfUpdatePhase.VerifyingPackage, "Проверка SHA256", ok: true);
            _store.Save(tx);

            var expected = ControlPlaneReleasePolicy.ParseSha256Sidecar(await File.ReadAllTextAsync(sumPath, cancellationToken));
            if (expected is null)
                throw new ValidationException(SelfUpdateResultCodes.PackageVerificationFailed + ": checksum parse");

            await using (var fs = File.OpenRead(zipPath))
            {
                var actual = ControlPlaneReleasePolicy.Sha256Hex(fs);
                if (!string.Equals(actual, expected, StringComparison.OrdinalIgnoreCase))
                    throw new ValidationException(SelfUpdateResultCodes.PackageVerificationFailed + ": sha mismatch");
                tx.PackageSha256 = actual;
            }

            var extractDir = Path.Combine(staging, "extracted");
            ControlPlaneReleasePolicy.ExtractZipSafe(zipPath, extractDir);
            if (!ControlPlaneReleasePolicy.PackageVersionMatches(extractDir, tx.TargetVersion))
                throw new ValidationException(SelfUpdateResultCodes.PackageVerificationFailed + ": VERSION mismatch");

            // Prefer publish/ payload if present in release zip.
            var publishDir = Path.Combine(extractDir, "publish");
            if (!Directory.Exists(publishDir))
                publishDir = extractDir;

            tx.StagingPath = publishDir;
            tx.Phase = SelfUpdatePhase.ReadyForHandoff;
            Mark(tx, SelfUpdatePhase.ReadyForHandoff, "Пакет проверен; передача updater", ok: true);
            _store.Save(tx);

            await _audit.WriteAsync(new AuditWriteRequest
            {
                Actor = actor,
                Action = "controlplane.update.started",
                EntityType = "ControlPlane",
                EntityId = tx.TransactionId.ToString("N"),
                Detail = $"sha256={tx.PackageSha256}"
            }, cancellationToken).ConfigureAwait(false);

            LaunchUpdater(tx);
            return tx;
        }
        catch (Exception ex)
        {
            _log.LogError(ex, "Control Plane self-update failed before handoff");
            tx.Phase = SelfUpdatePhase.Failed;
            tx.Status = SelfUpdateStatus.Failed;
            tx.ResultCode = SelfUpdateResultCodes.PackageVerificationFailed;
            tx.ResultMessage = Truncate(ex.Message, 400);
            tx.CompletedAt = _clock.UtcNow;
            Mark(tx, SelfUpdatePhase.Failed, tx.ResultMessage, ok: false);
            _store.AppendHistory(tx);
            await _audit.WriteAsync(new AuditWriteRequest
            {
                Actor = actor,
                Action = "controlplane.update.failed",
                EntityType = "ControlPlane",
                EntityId = tx.TransactionId.ToString("N"),
                Detail = tx.ResultCode
            }, cancellationToken).ConfigureAwait(false);
            throw;
        }
    }

    public Task<SelfUpdateTransaction?> GetActiveTransactionAsync(CancellationToken cancellationToken = default)
    {
        _ = cancellationToken;
        return Task.FromResult(_store.GetActive());
    }

    public Task<IReadOnlyList<SelfUpdateTransaction>> GetHistoryAsync(int take = 20, CancellationToken cancellationToken = default)
    {
        _ = cancellationToken;
        return Task.FromResult(_store.ListHistory(take));
    }

    public Task ReconcileOnStartupAsync(CancellationToken cancellationToken = default)
    {
        _ = cancellationToken;
        var active = _store.GetActive();
        if (active is null || active.Status != SelfUpdateStatus.InProgress)
            return Task.CompletedTask;

        var installed = ReadInstalledVersion();
        // Fail closed on ambiguous mid-install without updater ownership.
        if (active.Phase is SelfUpdatePhase.StoppingControlPlane or SelfUpdatePhase.Installing
            or SelfUpdatePhase.StartingControlPlane)
        {
            active.Phase = SelfUpdatePhase.Failed;
            active.Status = SelfUpdateStatus.Failed;
            active.ResultCode = SelfUpdateResultCodes.AmbiguousState;
            active.ResultMessage = "Incomplete self-update after restart; manual inspection required.";
            active.CompletedAt = _clock.UtcNow;
            Mark(active, SelfUpdatePhase.Failed, active.ResultMessage, ok: false);
            _store.AppendHistory(active);
            return Task.CompletedTask;
        }

        if (active.Phase is SelfUpdatePhase.HealthVerification or SelfUpdatePhase.Committing
            && string.Equals(installed, active.TargetVersion, StringComparison.Ordinal))
        {
            active.Phase = SelfUpdatePhase.Completed;
            active.Status = SelfUpdateStatus.Completed;
            active.ResultCode = SelfUpdateResultCodes.UpdatedHealthy;
            active.ResultMessage = "Recovered completed update after restart";
            active.CompletedAt = _clock.UtcNow;
            Mark(active, SelfUpdatePhase.Completed, active.ResultMessage, ok: true);
            _store.AppendHistory(active);
            return Task.CompletedTask;
        }

        if (active.Phase >= SelfUpdatePhase.ReadyForHandoff &&
            string.Equals(installed, active.CurrentVersion, StringComparison.Ordinal))
        {
            // Still on old version after handoff attempt — mark failed, operator can retry.
            active.Phase = SelfUpdatePhase.Failed;
            active.Status = SelfUpdateStatus.Failed;
            active.ResultCode = SelfUpdateResultCodes.HealthFailed;
            active.ResultMessage = "Updater handoff did not complete; installed version unchanged.";
            active.CompletedAt = _clock.UtcNow;
            Mark(active, SelfUpdatePhase.Failed, active.ResultMessage, ok: false);
            _store.AppendHistory(active);
        }

        return Task.CompletedTask;
    }

    private void LaunchUpdater(SelfUpdateTransaction tx)
    {
        var installDir = ResolveInstallDir();
        var handoffPath = Path.Combine(ServiceCollectionExtensions.GetProgramDataRoot(), "self-update", "handoff.json");
        Directory.CreateDirectory(Path.GetDirectoryName(handoffPath)!);
        File.WriteAllText(handoffPath, ControlPlaneReleasePolicy.BuildHandoffJson(tx, installDir ?? "", ServiceName));

        var updater = FindUpdaterBinary();
        if (updater is null)
        {
            // Fallback: invoke PowerShell apply script if present next to install / content root.
            var script = FindApplyScript();
            if (script is null)
                throw new InvalidOperationException("Nyxveil.ControlPlane.Updater.exe and self-update-apply.ps1 not found");

            var psi = new ProcessStartInfo
            {
                FileName = "powershell.exe",
                Arguments = $"-NoProfile -ExecutionPolicy Bypass -File \"{script}\" -HandoffPath \"{handoffPath}\"",
                UseShellExecute = false,
                CreateNoWindow = true
            };
            Process.Start(psi);
            return;
        }

        Process.Start(new ProcessStartInfo
        {
            FileName = updater,
            Arguments = $"--handoff \"{handoffPath}\"",
            UseShellExecute = false,
            CreateNoWindow = true
        });
    }

    private static string? FindUpdaterBinary()
    {
        var candidates = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "Nyxveil.ControlPlane.Updater.exe"),
            Path.Combine(AppContext.BaseDirectory, "updater", "Nyxveil.ControlPlane.Updater.exe"),
            Path.Combine(AppContext.BaseDirectory, "Nyxveil.ControlPlane.Updater.dll")
        };
        return candidates.FirstOrDefault(File.Exists);
    }

    private static string? FindApplyScript()
    {
        var candidates = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "scripts", "self-update-apply.ps1"),
            Path.Combine(AppContext.BaseDirectory, "..", "scripts", "self-update-apply.ps1"),
            Path.Combine(ServiceCollectionExtensions.GetProgramDataRoot(), "scripts", "self-update-apply.ps1")
        };
        return candidates.Select(Path.GetFullPath).FirstOrDefault(File.Exists);
    }

    private string ReadInstalledVersion()
    {
        foreach (var candidate in new[]
                 {
                     Path.Combine(AppContext.BaseDirectory, "VERSION"),
                     Path.Combine(ResolveInstallDir() ?? "", "VERSION"),
                     Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "..", "..", "VERSION"))
                 })
        {
            try
            {
                if (File.Exists(candidate))
                    return File.ReadAllText(candidate).Trim();
            }
            catch { /* continue */ }
        }

        return _configuration["Hosting:Version"] ?? "0.0.0";
    }

    private string? ResolveInstallDir()
    {
        var configured = _configuration["Hosting:InstallDir"];
        if (!string.IsNullOrWhiteSpace(configured) && Directory.Exists(configured))
            return configured;
        var baseDir = AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        return baseDir;
    }

    private static string ProbeServiceState()
    {
        if (!OperatingSystem.IsWindows()) return "n/a";
        try
        {
            var psi = new ProcessStartInfo
            {
                FileName = "sc.exe",
                Arguments = $"query {ServiceName}",
                RedirectStandardOutput = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };
            using var p = Process.Start(psi);
            if (p is null) return "unknown";
            var output = p.StandardOutput.ReadToEnd();
            p.WaitForExit(5000);
            if (output.Contains("RUNNING", StringComparison.OrdinalIgnoreCase)) return "Running";
            if (output.Contains("STOPPED", StringComparison.OrdinalIgnoreCase)) return "Stopped";
            return "unknown";
        }
        catch
        {
            return "unknown";
        }
    }

    private static async Task DownloadAsync(HttpClient client, string url, string path, CancellationToken ct)
    {
        await using var remote = await client.GetStreamAsync(url, ct).ConfigureAwait(false);
        await using var local = File.Create(path);
        await remote.CopyToAsync(local, ct).ConfigureAwait(false);
    }

    private void Mark(SelfUpdateTransaction tx, SelfUpdatePhase phase, string message, bool ok)
    {
        tx.Phase = phase;
        tx.ProgressMessage = message;
        tx.LastUpdatedAt = _clock.UtcNow;
        tx.Timeline.Add(new SelfUpdateTimelineEntry
        {
            At = tx.LastUpdatedAt,
            Phase = phase,
            Message = message,
            Ok = ok
        });
    }

    private static string Truncate(string s, int max) => s.Length <= max ? s : s[..max];
}
