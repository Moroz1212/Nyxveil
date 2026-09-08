namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IInfrastructureOverviewService
{
    Task<InfrastructureOverviewDto> GetOverviewAsync(CancellationToken cancellationToken = default);
}

public sealed class InfrastructureOverviewDto
{
    public ControlPlaneCertificateStatusDto ControlPlaneCertificate { get; set; } = new();
    public List<NodeCertificateHealthDto> Nodes { get; set; } = new();
    public List<RecentNodeCommandDto> RecentCommands { get; set; } = new();
}

public sealed class NodeCertificateHealthDto
{
    public string NodeId { get; set; } = string.Empty;
    public string DisplayName { get; set; } = string.Empty;
    public string? CertThumbprint { get; set; }
    public DateTime? CertNotAfter { get; set; }
    public int? DaysRemaining { get; set; }
    public string Health { get; set; } = "Unknown";
    public bool SupportsNodeCommands { get; set; }
    public string? ManagementCapabilities { get; set; }
    public int CurrentSessions { get; set; }
}

public sealed class RecentNodeCommandDto
{
    public Guid Id { get; set; }
    public string NodeId { get; set; } = string.Empty;
    public string Type { get; set; } = string.Empty;
    public string Status { get; set; } = string.Empty;
    public DateTime CreatedAt { get; set; }
    public string CreatedBy { get; set; } = string.Empty;
    public DateTime? CompletedAt { get; set; }
    public string? ResultCode { get; set; }
}
