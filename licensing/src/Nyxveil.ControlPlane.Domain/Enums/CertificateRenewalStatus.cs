namespace Nyxveil.ControlPlane.Domain.Enums;

public enum CertificateRenewalStatus
{
    PendingDns = 0,
    DnsReady = 1,
    Validating = 2,
    Issuing = 3,
    ReadyToImport = 4,
    Completed = 5,
    Failed = 6,
    Cancelled = 7,
    /// <summary>Detached tls configure / service restart in progress; wizard resumes after CP restart.</summary>
    Switching = 8,
    Expired = 9
}
