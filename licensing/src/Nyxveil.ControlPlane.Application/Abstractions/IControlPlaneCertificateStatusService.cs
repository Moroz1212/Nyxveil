namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IControlPlaneCertificateStatusService
{
    Task<ControlPlaneCertificateStatusDto> GetStatusAsync(CancellationToken cancellationToken = default);
}

public sealed class ControlPlaneCertificateStatusDto
{
    public string Hostname { get; set; } = string.Empty;
    public string? Subject { get; set; }
    public string? Issuer { get; set; }
    public string? San { get; set; }
    public DateTimeOffset? NotBefore { get; set; }
    public DateTimeOffset? NotAfter { get; set; }
    public int? DaysRemaining { get; set; }
    public string Health { get; set; } = "Unknown";
    public string? Thumbprint { get; set; }
    public string Store { get; set; } = string.Empty;
    public bool HasPrivateKey { get; set; }
    public bool SystemTrustOk { get; set; }
    public bool ServicePrivateKeyAccessOk { get; set; }
}
