namespace Nyxveil.ControlPlane.Domain.Enums;

public enum NodeVersionStatus
{
    Current = 0,
    UpdateAvailable = 1,
    Unsupported = 2,
    Ahead = 3,
    Unknown = 4,
    Invalid = 5,
    Updating = 6,
    UpdateFailed = 7
}
