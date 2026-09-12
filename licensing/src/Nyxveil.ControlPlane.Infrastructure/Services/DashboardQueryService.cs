using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class DashboardQueryService : IDashboardQueryService
{
    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly IClock _clock;
    private readonly IConfiguration? _configuration;
    private readonly CertificateExpiryOptions _expiry;
    private readonly ServerReleasePolicyOptions _releasePolicy;
    private readonly IServerReleaseService _releases;

    public DashboardQueryService(
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        IClock clock,
        IServerReleaseService releases,
        IConfiguration? configuration = null,
        IOptions<CertificateExpiryOptions>? expiry = null,
        IOptions<ServerReleasePolicyOptions>? releasePolicy = null)
    {
        _dbFactory = dbFactory;
        _clock = clock;
        _releases = releases;
        _configuration = configuration;
        _expiry = expiry?.Value ?? new CertificateExpiryOptions();
        _releasePolicy = releasePolicy?.Value ?? new ServerReleasePolicyOptions();
    }

    public async Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var expiringUntil = now.AddDays(14);
        var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
        var latest = release.LatestVersion;
        var minSupported = _releasePolicy.MinimumSupportedVersion;

        var nodes = await db.Nodes.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);
        var configs = await db.NodeConfigs.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);

        var active = nodes.Where(n => n.LifecycleState == NodeLifecycleState.Active).ToList();
        var versionStatuses = active.Select(n =>
        {
            var installed = NodeVersionEvaluator.EffectiveInstalledVersion(n.ReportedServerVersion, n.ServerVersion);
            return NodeVersionEvaluator.Evaluate(installed, latest, minSupported);
        }).ToList();

        var updateAvailable = versionStatuses.Count(s => s == NodeVersionStatus.UpdateAvailable);
        var unsupported = versionStatuses.Count(s => s == NodeVersionStatus.Unsupported);
        var current = versionStatuses.Count(s => s == NodeVersionStatus.Current);
        var unknown = versionStatuses.Count(s =>
            s is NodeVersionStatus.Unknown or NodeVersionStatus.Invalid);

        var summary = new DashboardSummary
        {
            ControlPlaneVersion = ReadControlPlaneVersion(),
            Hostname = _configuration?["Hosting:PublicHostname"] ?? Environment.MachineName,
            PublicUrl = _configuration?["Hosting:PublicBaseUrl"] ?? string.Empty,
            LocalPort = int.TryParse(_configuration?["Hosting:Port"], out var port) ? port : 8443,
            TlsStatus = string.IsNullOrWhiteSpace(_configuration?["Certificate:Thumbprint"]) ? "not configured" : "configured",
            ActiveLicenses = await db.Licenses.CountAsync(l => l.Status == LicenseStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            ExpiringLicenses = await db.Licenses.CountAsync(
                    l => l.Status == LicenseStatus.Active && l.ExpiresAt != null && l.ExpiresAt <= expiringUntil,
                    cancellationToken)
                .ConfigureAwait(false),
            RevokedLicenses = await db.Licenses.CountAsync(l => l.Status == LicenseStatus.Revoked, cancellationToken)
                .ConfigureAwait(false),
            ActiveDevices = await db.Devices.CountAsync(d => d.Status == DeviceStatus.Active, cancellationToken)
                .ConfigureAwait(false),
            TotalDevices = await db.Devices.CountAsync(cancellationToken).ConfigureAwait(false),
            HealthyNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Active && n.Status == NodeRuntimeStatus.Healthy),
            DegradedNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Active && n.Status == NodeRuntimeStatus.Degraded),
            OfflineNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Active && n.Status == NodeRuntimeStatus.Offline),
            OnlineNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Active &&
                (n.Status == NodeRuntimeStatus.Healthy || n.Status == NodeRuntimeStatus.Degraded)),
            TotalNodes = active.Count,
            DisabledNodes = active.Count(n => !n.Enabled),
            DrainingNodes = active.Count(n => n.Draining),
            MaintenanceNodes = configs.Count(c =>
                c.MaintenanceMode && active.Any(n => n.NodeId == c.NodeId)),
            DeletedNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Deleted),
            RevokedNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Revoked),
            CertificatesExpiring = active.Count(n =>
                CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry) is
                    CertificateHealthStatus.ExpiringSoon or CertificateHealthStatus.Critical),
            CertificatesExpired = active.Count(n =>
                CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry) == CertificateHealthStatus.Expired),
            StaleHeartbeatNodes = active.Count(n =>
                n.LastSeenAt == null || n.LastSeenAt < now.AddMinutes(-5)),
            OutdatedVersionNodes = updateAvailable + unsupported,
            UpdateAvailableNodes = updateAvailable,
            UnsupportedVersionNodes = unsupported,
            CurrentVersionNodes = current,
            UnknownVersionNodes = unknown,
            LatestServerVersion = latest,
            ActiveSessions = active.Sum(n => n.CurrentSessions),
            PendingBootstrapTokens = await db.BootstrapTokens.CountAsync(
                    t => t.Status == BootstrapTokenStatus.Active,
                    cancellationToken)
                .ConfigureAwait(false),
            CurrentSigningKeyId = await db.SigningKeysMetadata
                .Where(k => k.Status == SigningKeyStatus.Current)
                .Select(k => k.KeyId).FirstOrDefaultAsync(cancellationToken).ConfigureAwait(false),
            NextSigningKeyId = await db.SigningKeysMetadata
                .Where(k => k.Status == SigningKeyStatus.Next)
                .Select(k => k.KeyId).FirstOrDefaultAsync(cancellationToken).ConfigureAwait(false)
        };

        TryPopulateControlPlaneCertificate(summary, now);

        if (summary.CertificatesExpired > 0)
            summary.Warnings.Add($"РСЃС‚С‘РєС€РёС… СЃРµСЂС‚РёС„РёРєР°С‚РѕРІ СЃРµСЂРІРµСЂРѕРІ: {summary.CertificatesExpired}");
        if (summary.CertificatesExpiring > 0)
            summary.Warnings.Add($"РЎРµСЂС‚РёС„РёРєР°С‚С‹ СЃРµСЂРІРµСЂРѕРІ РёСЃС‚РµРєР°СЋС‚: {summary.CertificatesExpiring}");
        if (summary.StaleHeartbeatNodes > 0)
            summary.Warnings.Add($"РќРµС‚ СЃРІСЏР·Рё СЃ СЃРµСЂРІРµСЂР°РјРё: {summary.StaleHeartbeatNodes}");
        if (unsupported > 0)
            summary.Warnings.Add(unsupported == 1
                ? "1 СЃРµСЂРІРµСЂ РёСЃРїРѕР»СЊР·СѓРµС‚ РЅРµРїРѕРґРґРµСЂР¶РёРІР°РµРјСѓСЋ РІРµСЂСЃРёСЋ"
                : $"Р”Р»СЏ {unsupported} СЃРµСЂРІРµСЂРѕРІ С‚СЂРµР±СѓРµС‚СЃСЏ РѕР±РЅРѕРІР»РµРЅРёРµ (РЅРµРїРѕРґРґРµСЂР¶РёРІР°РµРјР°СЏ РІРµСЂСЃРёСЏ)");
        else if (updateAvailable > 0)
            summary.Warnings.Add(updateAvailable == 1
                ? "Р”Р»СЏ 1 СЃРµСЂРІРµСЂР° РґРѕСЃС‚СѓРїРЅРѕ РѕР±РЅРѕРІР»РµРЅРёРµ"
                : $"Р”Р»СЏ {updateAvailable} СЃРµСЂРІРµСЂРѕРІ РґРѕСЃС‚СѓРїРЅРѕ РѕР±РЅРѕРІР»РµРЅРёРµ");
        if (summary.CertificateHealth is "Expired" or "Critical" or "Invalid")
            summary.Warnings.Add($"РЎРµСЂС‚РёС„РёРєР°С‚ Control Plane: {summary.CertificateHealth}");
        else if (summary.CertificateHealth == "ExpiringSoon")
            summary.Warnings.Add("РЎРµСЂС‚РёС„РёРєР°С‚ Control Plane СЃРєРѕСЂРѕ РёСЃС‚РµС‡С‘С‚");
        return summary;
    }

    public async Task<IReadOnlyList<AttentionItem>> GetAttentionAsync(CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var heartbeat = new NodeHeartbeatOptions();
        var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
        var latest = release.LatestVersion;
        var minSupported = _releasePolicy.MinimumSupportedVersion;

        var nodes = await db.Nodes.AsNoTracking()
            .Where(n => n.LifecycleState != NodeLifecycleState.Deleted)
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        var health = await db.NodeHealth.AsNoTracking().ToDictionaryAsync(h => h.NodeId, cancellationToken)
            .ConfigureAwait(false);
        var configs = await db.NodeConfigs.AsNoTracking().ToDictionaryAsync(c => c.NodeId, cancellationToken)
            .ConfigureAwait(false);

        // Pull a bounded window; actionable filter applied in-memory (resolution beats age).
        var recentCommands = await db.NodeCommands.AsNoTracking()
            .Where(c => c.CompletedAt != null
                        && c.CompletedAt > now.AddDays(-7)
                        && c.ResultCode != null
                        && (c.Status == NodeCommandStatus.Failed
                            || c.Status == NodeCommandStatus.Expired))
            .OrderByDescending(c => c.CompletedAt)
            .Take(80)
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        var problemCommands = recentCommands
            .Where(c => AttentionCommandPolicy.IsActionableAttentionCommand(c.Status, c.ResultCode, c.CompletedAt, now))
            .Take(50)
            .ToList();

        // Supersede: if a newer UpdateNodeLatest succeeded for the same node, drop older
        // unknown/failed update attention items (history remains in operations).
        var successfulUpdates = await db.NodeCommands.AsNoTracking()
            .Where(c => c.Type == NodeCommandType.UpdateNodeLatest
                        && c.Status == NodeCommandStatus.Succeeded
                        && c.CompletedAt != null
                        && c.CompletedAt > now.AddDays(-7))
            .Select(c => new { c.NodeId, c.CompletedAt })
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        var latestSuccessByNode = successfulUpdates
            .GroupBy(x => x.NodeId, StringComparer.Ordinal)
            .ToDictionary(g => g.Key, g => g.Max(x => x.CompletedAt!.Value), StringComparer.Ordinal);
        problemCommands = problemCommands
            .Where(c =>
            {
                if (c.Type != NodeCommandType.UpdateNodeLatest)
                    return true;
                if (!latestSuccessByNode.TryGetValue(c.NodeId, out var okAt))
                    return true;
                return c.CompletedAt is null || c.CompletedAt >= okAt;
            })
            .ToList();

        var items = new List<AttentionItem>();
        string Name(Node n) => string.IsNullOrWhiteSpace(n.DisplayName) ? n.NodeId : n.DisplayName;

        foreach (var n in nodes.Where(n => n.LifecycleState == NodeLifecycleState.Active))
        {
            var h = health.GetValueOrDefault(n.NodeId);
            var cfg = configs.GetValueOrDefault(n.NodeId);
            var age = NodeFreshness.Age(n.LastSeenAt ?? h?.UpdatedAt, now);
            var ageText = NodeFreshness.FormatAgeRu(age);
            var href = $"/admin/nodes/{Uri.EscapeDataString(n.NodeId)}";

            if (n.Status == NodeRuntimeStatus.Offline || NodeFreshness.Evaluate(n.LastSeenAt, now, heartbeat) == DataFreshness.Stale)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Critical,
                    Title = AttentionCopy.ServerOffline,
                    Detail = AttentionCopy.NoFreshHeartbeat,
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = href,
                    Kind = "offline"
                });
            }
            else if (n.Status == NodeRuntimeStatus.Degraded)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Warning,
                    Title = AttentionCopy.Degraded,
                    Detail = "РЎС‚Р°С‚СѓСЃ Degraded.",
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = href,
                    Kind = "degraded"
                });
            }

            if (h?.TlsOk == false)
                items.Add(MakeRuntime(n, Name(n), ageText, href, "TLS runtime FAIL", AttentionSeverity.Critical, "tls"));
            if (h?.QuicOk == false)
                items.Add(MakeRuntime(n, Name(n), ageText, href, "QUIC runtime FAIL", AttentionSeverity.Critical, "quic"));
            if (h?.TunReady == false)
                items.Add(MakeRuntime(n, Name(n), ageText, href, "TUN FAIL", AttentionSeverity.Critical, "tun"));
            if (h?.CpConnected == false)
                items.Add(MakeRuntime(n, Name(n), ageText, href, AttentionCopy.CpDisconnected, AttentionSeverity.Critical, "cp"));

            var cert = CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry);
            if (cert == CertificateHealthStatus.Expired)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Critical,
                    Title = AttentionCopy.CertExpired,
                    Detail = AttentionCopy.CertExpiredDetail,
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = "/admin/infrastructure",
                    Kind = "cert_expired"
                });
            }
            else if (cert is CertificateHealthStatus.Critical or CertificateHealthStatus.ExpiringSoon)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Warning,
                    Title = "РЎРµСЂС‚РёС„РёРєР°С‚ СЃРєРѕСЂРѕ РёСЃС‚РµС‡С‘С‚",
                    Detail = $"РћСЃС‚Р°Р»РѕСЃСЊ РґРЅРµР№: {CertificateExpiry.DaysRemaining(n.CertNotAfter, now)?.ToString() ?? "вЂ”"}",
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = "/admin/infrastructure",
                    Kind = "cert_expiring"
                });
            }

            var installed = NodeVersionEvaluator.EffectiveInstalledVersion(n.ReportedServerVersion, n.ServerVersion);
            var ver = NodeVersionEvaluator.Evaluate(installed, latest, minSupported);
            if (ver == NodeVersionStatus.Unsupported)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Critical,
                    Title = "РќРµРїРѕРґРґРµСЂР¶РёРІР°РµРјР°СЏ РІРµСЂСЃРёСЏ",
                    Detail = $"РЈСЃС‚Р°РЅРѕРІР»РµРЅРѕ: {installed ?? "вЂ”"}",
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = href,
                    Kind = "unsupported"
                });
            }
            else if (ver == NodeVersionStatus.UpdateAvailable)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Info,
                    Title = "Р”РѕСЃС‚СѓРїРЅРѕ РѕР±РЅРѕРІР»РµРЅРёРµ",
                    Detail = $"{installed} в†’ {latest}",
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = href,
                    Kind = "update"
                });
            }

            if (n.Draining && cfg?.MaintenanceMode != true)
            {
                items.Add(new AttentionItem
                {
                    Severity = AttentionSeverity.Info,
                    Title = "Р—Р°РІРµСЂС€РµРЅРёРµ СЃРµР°РЅСЃРѕРІ (Drain)",
                    Detail = "РЎРµСЂРІРµСЂ РЅРµ РїСЂРёРЅРёРјР°РµС‚ РЅРѕРІС‹Рµ РїРѕРґРєР»СЋС‡РµРЅРёСЏ.",
                    NodeId = n.NodeId,
                    NodeName = Name(n),
                    LocationId = n.LocationId,
                    AgeText = ageText,
                    Href = href,
                    Kind = "draining"
                });
            }
        }

        var visibleNodeIds = nodes.Select(n => n.NodeId).ToHashSet(StringComparer.Ordinal);
        foreach (var c in problemCommands.Where(c => visibleNodeIds.Contains(c.NodeId)))
        {
            var n = nodes.First(x => x.NodeId == c.NodeId);
            var sev = AttentionCommandPolicy.IsUnknownOutcome(c.ResultCode)
                ? AttentionSeverity.Critical
                : AttentionSeverity.Warning;
            items.Add(new AttentionItem
            {
                Severity = sev,
                Title = AttentionCommandPolicy.IsUnknownOutcome(c.ResultCode)
                    ? AttentionCopy.UnknownUpdateOutcome
                    : AttentionCopy.OperationFailed,
                Detail = $"{c.Type}: {c.ResultCode}",
                NodeId = c.NodeId,
                NodeName = Name(n),
                LocationId = n.LocationId,
                AgeText = NodeFreshness.FormatAgeRu(c.CompletedAt is null ? null : now - c.CompletedAt.Value),
                Href = "/admin/operations",
                Kind = "command"
            });
        }

        return items
            .OrderByDescending(i => i.Severity)
            .ThenBy(i => i.NodeName)
            .Take(100)
            .ToList();
    }

    private static AttentionItem MakeRuntime(Node n, string name, string ageText, string href, string title,
        AttentionSeverity severity, string kind) => new()
    {
        Severity = severity,
        Title = title,
        Detail = "РџРѕ РґР°РЅРЅС‹Рј РїРѕСЃР»РµРґРЅРµРіРѕ health-РѕС‚С‡С‘С‚Р°.",
        NodeId = n.NodeId,
        NodeName = name,
        LocationId = n.LocationId,
        AgeText = ageText,
        Href = href,
        Kind = kind
    };

    private string ReadControlPlaneVersion()
    {
        try
        {
            var dir = AppContext.BaseDirectory;
            for (var i = 0; i < 8 && !string.IsNullOrEmpty(dir); i++)
            {
                var candidate = Path.Combine(dir, "VERSION");
                if (File.Exists(candidate))
                    return File.ReadAllText(candidate).Trim();
                var parent = Directory.GetParent(dir)?.FullName;
                if (parent is null || parent == dir) break;
                dir = parent;
            }
        }
        catch
        {
        }

        return "1.3.10";
    }

    private void TryPopulateControlPlaneCertificate(DashboardSummary summary, DateTime now)
    {
        try
        {
            var installDir = _configuration?["Hosting:InstallDir"]
                             ?? AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            if (string.IsNullOrWhiteSpace(installDir) || !Directory.Exists(installDir))
                return;

            var report = TlsConfigureEngine.CreateDefault().Status(installDir);
            summary.Hostname = string.IsNullOrWhiteSpace(report.PublicHostname) ? summary.Hostname : report.PublicHostname;
            summary.PublicUrl = string.IsNullOrWhiteSpace(report.PublicBaseUrl) ? summary.PublicUrl : report.PublicBaseUrl;
            summary.LocalPort = report.LocalListenPort > 0 ? report.LocalListenPort : summary.LocalPort;
            summary.CertificateSubject = report.Subject;
            summary.CertificateSan = report.San;
            summary.CertificateIssuer = report.Issuer;
            summary.CertificateThumbprint = report.Thumbprint;
            summary.CertificateNotBefore = report.NotBefore;
            summary.CertificateNotAfter = report.NotAfter;
            summary.CertificatePrivateKeyPresent = report.HasPrivateKey;
            summary.CertificateServiceKeyAccess = report.ServiceAccountPrivateKeyAccess;
            summary.CertificateTrustMode = string.IsNullOrWhiteSpace(report.ValidationMode)
                ? report.PublicTrustResult
                : report.ValidationMode;
            summary.CertificateDaysRemaining = CertificateExpiry.DaysRemaining(report.NotAfter?.UtcDateTime, now);
            summary.CertificateHealth = CertificateExpiry.Evaluate(report.NotAfter?.UtcDateTime, now, _expiry).ToString();
            summary.TlsStatus = summary.CertificateHealth;
        }
        catch
        {
            // Dashboard must remain available even if TLS status probe fails.
        }
    }
}
