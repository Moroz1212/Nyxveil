namespace Nyxveil.ControlPlane.Web.Security;

public static class AuthPolicies
{
    public const string AnyAdmin = "AnyAdmin";
    public const string OperatorOrAbove = "OperatorOrAbove";
    public const string SuperAdminOnly = "SuperAdminOnly";
}
