namespace Nyxveil.ControlPlane.Web.Security;

/// <summary>
/// Paths a SuperAdmin may access before MFA is enabled.
/// </summary>
public static class MfaPathRules
{
    public static bool IsExempt(PathString path)
    {
        var p = path.Value ?? string.Empty;
        if (p.Length == 0)
            return false;

        if (StartsWith(p, "/account/mfa")
            || StartsWith(p, "/account/logout")
            || StartsWith(p, "/account/login")
            || StartsWith(p, "/account/access-denied")
            || StartsWith(p, "/setup")
            || StartsWith(p, "/_framework")
            || StartsWith(p, "/_blazor")
            || StartsWith(p, "/_content")
            || StartsWith(p, "/health")
            || StartsWith(p, "/hubs")
            || StartsWith(p, "/api")
            || StartsWith(p, "/swagger")
            || StartsWith(p, "/.well-known"))
        {
            return true;
        }

        return HasStaticExtension(p);
    }

    private static bool StartsWith(string path, string prefix) =>
        path.StartsWith(prefix, StringComparison.OrdinalIgnoreCase);

    private static bool HasStaticExtension(string path)
    {
        return path.EndsWith(".css", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".js", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".map", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".ico", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".png", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".svg", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".jpg", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".jpeg", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".webp", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".woff", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".woff2", StringComparison.OrdinalIgnoreCase)
               || path.EndsWith(".ttf", StringComparison.OrdinalIgnoreCase);
    }
}
