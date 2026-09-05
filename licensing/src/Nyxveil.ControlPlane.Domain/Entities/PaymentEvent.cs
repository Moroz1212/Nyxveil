using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class PaymentEvent
{
    public Guid Id { get; set; }

    [MaxLength(64)]
    public string Provider { get; set; } = string.Empty;

    [MaxLength(256)]
    public string ExternalPaymentId { get; set; } = string.Empty;

    public PaymentEventStatus Status { get; set; } = PaymentEventStatus.Received;

    public decimal? Amount { get; set; }

    [MaxLength(8)]
    public string? Currency { get; set; }

    [MaxLength(128)]
    public string? PayloadHash { get; set; }

    public DateTime ReceivedAt { get; set; }

    public DateTime? ProcessedAt { get; set; }
}
