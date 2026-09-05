using Microsoft.AspNetCore.Http;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Auth;

public static class AuthTokenExtractor
{
    public const string LicenseTokenHeader = "X-License-Token";
    public const string NodeIdHeader = "X-Node-Id";
    public const string NodeIdHeaderAlt = "X-Node-ID";

    public static string? ExtractBearer(HttpRequest request)
    {
        var auth = request.Headers.Authorization.ToString();
        if (string.IsNullOrWhiteSpace(auth))
            return null;

        const string prefix = "Bearer ";
        if (!auth.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            return null;

        var token = auth[prefix.Length..].Trim();
        return string.IsNullOrEmpty(token) ? null : token;
    }

    /// <summary>
    /// License credentials: Authorization Bearer {licenseId:secret} or X-License-Token.
    /// For catalog auth, Bearer may also be an access ticket JWT.
    /// </summary>
    public static string? ExtractLicenseOrBearerToken(HttpRequest request)
    {
        var fromHeader = request.Headers[LicenseTokenHeader].ToString();
        if (!string.IsNullOrWhiteSpace(fromHeader))
            return fromHeader.Trim();

        return ExtractBearer(request);
    }

    public static string? ExtractNodeId(HttpRequest request)
    {
        var id = request.Headers[NodeIdHeader].ToString();
        if (string.IsNullOrWhiteSpace(id))
            id = request.Headers[NodeIdHeaderAlt].ToString();
        if (string.IsNullOrWhiteSpace(id))
            id = request.Query["node_id"].ToString();
        return string.IsNullOrWhiteSpace(id) ? null : id.Trim();
    }

    /// <summary>
    /// Core lookalike: id:secret without JWT dots.
    /// </summary>
    public static bool LooksLikeLicenseToken(string? token)
    {
        if (string.IsNullOrWhiteSpace(token))
            return false;

        var idx = token.IndexOf(':');
        if (idx <= 0 || idx >= token.Length - 1)
            return false;

        return !token.Contains('.', StringComparison.Ordinal);
    }

    public static string? GetLicenseToken(HttpContext http) =>
        http.Items.TryGetValue(AuthContextKeys.LicenseToken, out var v) ? v as string : null;

    public static AccessTicketClaims? GetTicketClaims(HttpContext http) =>
        http.Items.TryGetValue(AuthContextKeys.TicketClaims, out var v) ? v as AccessTicketClaims : null;

    public static string? GetNodeId(HttpContext http) =>
        http.Items.TryGetValue(AuthContextKeys.NodeId, out var v) ? v as string : null;
}
