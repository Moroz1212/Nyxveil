namespace Nyxveil.ControlPlane.Api.Auth;

/// <summary>HttpContext.Items keys and claim types for Control Plane API auth.</summary>
public static class AuthContextKeys
{
    public const string LicenseToken = "nvp.license_token";
    public const string TicketClaims = "nvp.ticket_claims";
    public const string NodeId = "nvp.node_id";
    public const string AuthKind = "nvp.auth_kind";

    public const string AuthKindLicense = "license";
    public const string AuthKindTicket = "ticket";
    public const string AuthKindNode = "node";
}
