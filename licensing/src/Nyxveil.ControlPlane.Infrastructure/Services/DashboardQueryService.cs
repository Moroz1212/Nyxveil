using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
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

    public DashboardQueryService(
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        IClock clock,
        IConfiguration? configuration = null,
        IOptions<CertificateExpiryOptions>? expiry = null)
    {
        _dbFactory = dbFactory;
        _clock = clock;
        _configuration = configuration;
        _expiry = expiry?.Value ?? new CertificateExpiryOptions();
    }

    public async Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var expiringUntil = now.AddDays(14);
        const string currentServerRelease = "1.1.0";

        var nodes = await db.Nodes.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);
        var configs = await db.NodeConfigs.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);
        var summary = new DashboardSummary
        {
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
            TotalNodes = nodes.Count,
            DisabledNodes = nodes.Count(n => !n.Enabled),
            DrainingNodes = nodes.Count(n => n.Draining),
            MaintenanceNodes = configs.Count(c => c.MaintenanceMode),
            DeletedNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Deleted),
            RevokedNodes = nodes.Count(n => n.LifecycleState == NodeLifecycleState.Revoked),
            CertificatesExpiring = nodes.Count(n =>
                CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry) is
                    CertificateHealthStatus.ExpiringSoon or CertificateHealthStatus.Critical),
            CertificatesExpired = nodes.Count(n =>
                CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry) == CertificateHealthStatus.Expired),
            StaleHeartbeatNodes = nodes.Count(n =>
                n.LifecycleState == NodeLifecycleState.Active && (n.LastSeenAt == null || n.LastSeenAt < now.AddMinutes(-5))),
            OutdatedVersionNodes = nodes.Count(n =>
                n.LifecycleState == NodeLifecycleState.Active &&
                !string.IsNullOrWhiteSpace(n.ServerVersion) &&
                !string.Equals(n.ServerVersion, currentServerRelease, StringComparison.OrdinalIgnoreCase)),
            ActiveSessions = nodes.Where(n => n.LifecycleState == NodeLifecycleState.Active).Sum(n => n.CurrentSessions),
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

        if (summary.CertificatesExpired > 0) summary.Warnings.Add($"{summary.CertificatesExpired} node certificate(s) expired");
        if (summary.CertificatesExpiring > 0) summary.Warnings.Add($"{summary.CertificatesExpiring} node certificate(s) expiring");
        if (summary.StaleHeartbeatNodes > 0) summary.Warnings.Add($"{summary.StaleHeartbeatNodes} node heartbeat(s) stale");
        if (summary.OutdatedVersionNodes > 0) summary.Warnings.Add($"{summary.OutdatedVersionNodes} node version(s) outdated");
        if (summary.CertificateHealth is "Expired" or "Critical" or "Invalid")
            summary.Warnings.Add($"Control Plane certificate: {summary.CertificateHealth}");
        else if (summary.CertificateHealth == "ExpiringSoon")
            summary.Warnings.Add("Control Plane certificate expiring soon");
        return summary;
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
