namespace Nyxveil.ControlPlane.Domain.Enums;

/// <summary>
/// Administrative lifecycle for VPN nodes. Distinct from <see cref="NodeRuntimeStatus"/> (health).
/// Deleted/Revoked nodes must not reappear via heartbeat or same-node re-register.
/// </summary>
public enum NodeLifecycleState
{
    Active = 0,
    Revoked = 1,
    Deleted = 2
}
