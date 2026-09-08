using Nyxveil.ControlPlane.Domain.Entities;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IControlPlaneAcmeWizardService
{
    Task<CertificateRenewalOperation> StartAsync(string actor, CancellationToken cancellationToken = default);

    Task<CertificateRenewalOperation?> GetActiveAsync(CancellationToken cancellationToken = default);

    Task<CertificateRenewalOperation> GetStatusAsync(Guid id, CancellationToken cancellationToken = default);

    Task<DnsTxtVerifyResult> VerifyDnsTxtAsync(Guid id, CancellationToken cancellationToken = default);

    Task<CertificateRenewalOperation> ContinueFinalizeAsync(Guid id, CancellationToken cancellationToken = default);

    Task<CertificateRenewalOperation> ImportAndSwitchAsync(Guid id, CancellationToken cancellationToken = default);
}

public sealed class DnsTxtVerifyResult
{
    public bool Found { get; set; }
    public string? ObservedValue { get; set; }
    public string Message { get; set; } = string.Empty;
}

/// <summary>
/// ACME DNS-01 provider abstraction. Real Certes implementation behind Acme:UseStaging;
/// fakes used in unit tests. Account private keys stay on disk — never in DB.
/// </summary>
public interface IAcmeDns01Provider
{
    Task<AcmeDns01Order> CreateDns01OrderAsync(string domain, CancellationToken cancellationToken = default);

    Task ValidateChallengeAsync(string orderUrl, CancellationToken cancellationToken = default);

    Task<AcmeIssuedCertificate> FinalizeAsync(string orderUrl, CancellationToken cancellationToken = default);
}

public sealed class AcmeDns01Order
{
    public string OrderUrl { get; set; } = string.Empty;
    public string ChallengeName { get; set; } = string.Empty;
    public string ChallengeValue { get; set; } = string.Empty;
    public DateTime? ChallengeExpiresAt { get; set; }
}

public sealed class AcmeIssuedCertificate
{
    /// <summary>Public certificate PEM (no private key).</summary>
    public string CertificatePem { get; set; } = string.Empty;

    public string Thumbprint { get; set; } = string.Empty;

    /// <summary>Path to PFX on disk when issued (private key never stored in DB).</summary>
    public string? PfxPath { get; set; }
}
