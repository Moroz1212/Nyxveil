namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IDashboardQueryService
{
    Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default);
}

public sealed class DashboardSummary
{
    public string ControlPlaneVersion { get; set; } = "1.3.1";
    public string Hostname { get; set; } = string.Empty;
    public string PublicUrl { get; set; } = string.Empty;
    public int LocalPort { get; set; } = 8443;
    public string TlsStatus { get; set; } = "unknown";
    public string? CertificateSubject { get; set; }
    public string? CertificateSan { get; set; }
    public string? CertificateIssuer { get; set; }
    public string? CertificateThumbprint { get; set; }
    public DateTimeOffset? CertificateNotBefore { get; set; }
    public DateTimeOffset? CertificateNotAfter { get; set; }
    public int? CertificateDaysRemaining { get; set; }
    public string CertificateHealth { get; set; } = "Unknown";
    public string CertificateTrustMode { get; set; } = string.Empty;
    public bool CertificatePrivateKeyPresent { get; set; }
    public string CertificateServiceKeyAccess { get; set; } = string.Empty;
    public int ActiveLicenses { get; set; }
    public int ExpiringLicenses { get; set; }
    public int RevokedLicenses { get; set; }
    public int ActiveDevices { get; set; }
    public int TotalDevices { get; set; }
    public int HealthyNodes { get; set; }
    public int DegradedNodes { get; set; }
    public int OfflineNodes { get; set; }
    public int OnlineNodes { get; set; }
    public int TotalNodes { get; set; }
    public int DisabledNodes { get; set; }
    public int DrainingNodes { get; set; }
    public int MaintenanceNodes { get; set; }
    public int DeletedNodes { get; set; }
    public int RevokedNodes { get; set; }
    public int CertificatesExpiring { get; set; }
    public int CertificatesExpired { get; set; }
    public int StaleHeartbeatNodes { get; set; }
    public int OutdatedVersionNodes { get; set; }
    public int UpdateAvailableNodes { get; set; }
    public int UnsupportedVersionNodes { get; set; }
    public int CurrentVersionNodes { get; set; }
    public int UnknownVersionNodes { get; set; }
    public string? LatestServerVersion { get; set; }
    public int ActiveSessions { get; set; }
    public int PendingBootstrapTokens { get; set; }
    public string? CurrentSigningKeyId { get; set; }
    public string? NextSigningKeyId { get; set; }
    public List<string> Warnings { get; set; } = new();
}
