using Nyxveil.ControlPlane.Domain.Entities;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public enum UnknownUpdateReconciliationAction
{
    ConfirmRollback = 1,
    ConfirmUpdated = 2
}

/// <summary>Operator evidence preview before manual reconciliation of an unknown UpdateNodeLatest outcome.</summary>
public sealed class UnknownUpdateReconciliationPreview
{
    public Guid CommandId { get; set; }
    public string NodeId { get; set; } = string.Empty;
    public string? PreviousVersion { get; set; }
    public string? TargetVersion { get; set; }
    public string? ObservedVersion { get; set; }
    public string? ResultCode { get; set; }
    public string? ResultMessage { get; set; }
    public string Status { get; set; } = string.Empty;
    public DateTime? LastSeenAt { get; set; }
    public bool HeartbeatFresh { get; set; }
    public bool Draining { get; set; }
    public bool Enabled { get; set; }
    public bool MaintenanceMode { get; set; }
    public int CurrentSessions { get; set; }
    public long ConfigVersion { get; set; }
    public string LifecycleState { get; set; } = string.Empty;
    public string RuntimeStatus { get; set; } = string.Empty;
    public bool HasAdminStateSnapshot { get; set; }
    public bool AlreadyReconciled { get; set; }
    public bool CanConfirmRollback { get; set; }
    public bool CanConfirmUpdated { get; set; }
    public string? BlockingReason { get; set; }
}

public sealed class UnknownUpdateReconciliationRequest
{
    public Guid CommandId { get; set; }
    public string NodeId { get; set; } = string.Empty;
    public UnknownUpdateReconciliationAction Action { get; set; }
    public string Actor { get; set; } = string.Empty;
    public IReadOnlyList<string> Roles { get; set; } = Array.Empty<string>();
    public string Reason { get; set; } = string.Empty;
}

public sealed class UnknownUpdateReconciliationResult
{
    public NodeCommand Command { get; set; } = null!;
    public bool IdempotentReplay { get; set; }
}
