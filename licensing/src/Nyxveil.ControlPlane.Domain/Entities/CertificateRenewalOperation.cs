using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

/// <summary>
/// Durable Control Plane DNS-01 certificate renewal wizard state.
/// Never stores ACME account private keys or certificate private keys.
/// </summary>
public class CertificateRenewalOperation
{
    public Guid Id { get; set; }

    public CertificateRenewalStatus Status { get; set; } = CertificateRenewalStatus.PendingDns;

    [MaxLength(256)]
    public string Domain { get; set; } = string.Empty;

    public DateTime CreatedAt { get; set; }

    [MaxLength(256)]
    public string CreatedBy { get; set; } = string.Empty;

    public DateTime UpdatedAt { get; set; }

    [MaxLength(1024)]
    public string? AcmeOrderUrl { get; set; }

    [MaxLength(256)]
    public string ChallengeName { get; set; } = string.Empty;

    [MaxLength(512)]
    public string ChallengeValue { get; set; } = string.Empty;

    public DateTime? ChallengeExpiresAt { get; set; }

    [MaxLength(128)]
    public string? NewThumbprint { get; set; }

    [MaxLength(128)]
    public string? OldThumbprint { get; set; }

    [MaxLength(1024)]
    public string? ErrorMessage { get; set; }

    public DateTime? CompletedAt { get; set; }
}
