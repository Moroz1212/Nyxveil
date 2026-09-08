namespace Nyxveil.ControlPlane.Domain.Enums;

/// <summary>
/// Node command lifecycle. Core path: Pending → Claimed → Running → Succeeded|Failed.
/// RebootHost may pass through Accepted → NodeReturned before Succeeded.
/// </summary>
public enum NodeCommandStatus
{
    Pending = 0,
    Claimed = 1,
    Running = 2,
    Succeeded = 3,
    Failed = 4,
    Expired = 5,
    Cancelled = 6,
    /// <summary>Reboot accepted by node; waiting for boot_id change / NodeReturned.</summary>
    Accepted = 7,
    Executing = 8,
    NodeOffline = 9,
    /// <summary>Host returned after reboot (boot_id changed); maps to Succeeded.</summary>
    NodeReturned = 10
}
