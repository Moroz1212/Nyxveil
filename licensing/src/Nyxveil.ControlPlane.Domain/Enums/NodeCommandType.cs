namespace Nyxveil.ControlPlane.Domain.Enums;

public enum NodeCommandType
{
    RenewCertificate = 0,
    RestartNyxveilService = 1,
    RebootHost = 2,
    UpdateNodeLatest = 3
}
