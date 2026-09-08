using System.ComponentModel.DataAnnotations;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Domain.Entities;

public class NodeCommand
{
    public Guid Id { get; set; }

    [MaxLength(128)]
    public string NodeId { get; set; } = string.Empty;

    public NodeCommandType Type { get; set; }

    public NodeCommandStatus Status { get; set; } = NodeCommandStatus.Pending;

    public DateTime CreatedAt { get; set; }

    [MaxLength(256)]
    public string CreatedBy { get; set; } = string.Empty;

    public DateTime IssuedAt { get; set; }

    public DateTime ExpiresAt { get; set; }

    public DateTime? ClaimedAt { get; set; }

    public DateTime? StartedAt { get; set; }

    public DateTime? CompletedAt { get; set; }

    [MaxLength(64)]
    public string? ResultCode { get; set; }

    [MaxLength(1024)]
    public string? ResultMessage { get; set; }

    public int AttemptCount { get; set; }

    public Guid CorrelationId { get; set; }

    /// <summary>Optional non-secret command payload JSON.</summary>
    public string? PayloadJson { get; set; }

    [MaxLength(64)]
    public string? ProgressPhase { get; set; }

    [MaxLength(512)]
    public string? ProgressMessage { get; set; }

    public DateTime? ProgressUpdatedAt { get; set; }

    [MaxLength(64)]
    public string? PreviousVersion { get; set; }

    [MaxLength(64)]
    public string? TargetVersion { get; set; }

    public Node Node { get; set; } = null!;
}
