namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IDashboardQueryService
{
    Task<DashboardSummary> GetSummaryAsync(CancellationToken cancellationToken = default);
}

public sealed class DashboardSummary
{
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
    public int ActiveSessions { get; set; }
    public int PendingBootstrapTokens { get; set; }
}
