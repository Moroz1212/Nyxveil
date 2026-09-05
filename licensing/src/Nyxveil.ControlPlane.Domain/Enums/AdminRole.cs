namespace Nyxveil.ControlPlane.Domain.Enums;

/// <summary>
/// Admin role names used with Identity claims.
/// Prefer these constants when assigning or checking claim values.
/// </summary>
public static class AdminRole
{
    public const string SuperAdmin = "SuperAdmin";
    public const string Operator = "Operator";
    public const string ReadOnly = "ReadOnly";
}

public enum AdminRoleKind
{
    SuperAdmin = 0,
    Operator = 1,
    ReadOnly = 2
}
