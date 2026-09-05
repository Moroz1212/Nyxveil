using System.Text.Json;
using Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Application.Tickets;

/// <summary>
/// Frozen NVP/1 ticket refresh scope rules — never widens security grants.
/// Mirrors Go core/controlplane/server/ticket_refresh.go.
/// </summary>
public static class TicketScopeCalculator
{
    public const string PermissionConnect = "connect";

    private static readonly HashSet<string> AllowedRoles = new(StringComparer.OrdinalIgnoreCase)
    {
        "user", "master", "test"
    };

    /// <summary>Normalize license role to Frozen allowlist (user / master / test).</summary>
    public static string NormalizeRole(string? licenseRole)
    {
        if (string.IsNullOrWhiteSpace(licenseRole))
            return "user";

        var trimmed = licenseRole.Trim();
        if (!AllowedRoles.Contains(trimmed))
            throw new ValidationException($"unsupported license role '{licenseRole}'");

        if (string.Equals(trimmed, "master", StringComparison.OrdinalIgnoreCase))
            return "master";
        if (string.Equals(trimmed, "test", StringComparison.OrdinalIgnoreCase))
            return "test";
        return "user";
    }

    /// <summary>Deprecated plan→role mapping; prefer <see cref="NormalizeRole"/> from License.Role.</summary>
    public static string RoleForPlan(string plan) =>
        string.Equals(plan, "master", StringComparison.OrdinalIgnoreCase) ? "master" : "user";

    /// <summary>Parse Plan.Permissions JSON; empty / invalid → empty list.</summary>
    public static IReadOnlyList<string> PermissionsFromPlanJson(string? permissionsJson)
    {
        if (string.IsNullOrWhiteSpace(permissionsJson))
            return Array.Empty<string>();

        try
        {
            var parsed = JsonSerializer.Deserialize<string[]>(permissionsJson);
            if (parsed is null || parsed.Length == 0)
                return Array.Empty<string>();

            var seen = new HashSet<string>(StringComparer.Ordinal);
            var list = new List<string>();
            foreach (var p in parsed)
            {
                if (string.IsNullOrWhiteSpace(p))
                    continue;
                if (seen.Add(p))
                    list.Add(p);
            }

            return list;
        }
        catch (JsonException)
        {
            return Array.Empty<string>();
        }
    }

    public static bool HasConnectPermission(IReadOnlyList<string> permissions)
    {
        foreach (var p in permissions)
        {
            if (string.Equals(p, PermissionConnect, StringComparison.Ordinal))
                return true;
        }

        return false;
    }

    /// <summary>Hardcoded connect-only fallback (legacy tests). Prefer <see cref="PermissionsFromPlanJson"/>.</summary>
    public static IReadOnlyList<string> PermissionsForPlan(string plan)
    {
        _ = plan;
        return new[] { PermissionConnect };
    }

    /// <summary>
    /// Locations = intersection(old, currentAllowed).
    /// If license unrestricted (allowed empty): keep old.
    /// If old unrestricted but license now restricted: take license allowlist.
    /// </summary>
    public static IReadOnlyList<string> RefreshLocations(
        IReadOnlyList<string>? oldLocations,
        IReadOnlyList<string>? currentAllowed)
    {
        var old = Normalize(oldLocations);
        var allowed = Normalize(currentAllowed);

        if (allowed.Count == 0)
            return old;

        if (old.Count == 0)
            return allowed;

        return IntersectPreserveOrder(old, allowed);
    }

    /// <summary>
    /// NodeScope: empty old → stay empty (location-scoped).
    /// Empty allowedNodes → preserve old (do not clear).
    /// Else intersection; empty intersection rejects.
    /// </summary>
    public static IReadOnlyList<string> RefreshNodeScope(
        IReadOnlyList<string>? oldNodeScope,
        IReadOnlyList<string>? allowedNodes)
    {
        var old = Normalize(oldNodeScope);
        if (old.Count == 0)
            return Array.Empty<string>();

        var allowed = Normalize(allowedNodes);
        if (allowed.Count == 0)
            return old;

        var intersection = IntersectPreserveOrder(old, allowed);
        if (intersection.Count == 0)
            throw new TicketScopeRejectedException("refresh left empty node scope intersection");

        return intersection;
    }

    public static bool TryRefreshLocations(
        IReadOnlyList<string>? oldLocations,
        IReadOnlyList<string>? currentAllowed,
        out IReadOnlyList<string> result,
        out string? error)
    {
        result = RefreshLocations(oldLocations, currentAllowed);
        var allowed = Normalize(currentAllowed);
        if (allowed.Count > 0 && result.Count == 0)
        {
            error = "refresh left no allowed locations";
            return false;
        }

        error = null;
        return true;
    }

    public static bool TryRefreshNodeScope(
        IReadOnlyList<string>? oldNodeScope,
        IReadOnlyList<string>? allowedNodes,
        out IReadOnlyList<string> result,
        out string? error)
    {
        try
        {
            result = RefreshNodeScope(oldNodeScope, allowedNodes);
            error = null;
            return true;
        }
        catch (TicketScopeRejectedException ex)
        {
            result = Array.Empty<string>();
            error = ex.Message;
            return false;
        }
    }

    /// <summary>
    /// Full refresh scope from CURRENT role/permissions + non-widening location/node intersections.
    /// </summary>
    public static bool TryComputeRefreshScope(
        string? currentLicenseRole,
        string? currentPlanPermissionsJson,
        IReadOnlyList<string>? oldLocations,
        IReadOnlyList<string>? currentAllowedLocations,
        IReadOnlyList<string>? oldNodeScope,
        IReadOnlyList<string>? allowedNodes,
        out string role,
        out IReadOnlyList<string> permissions,
        out IReadOnlyList<string> locations,
        out IReadOnlyList<string> nodeScope,
        out string? error)
    {
        try
        {
            role = NormalizeRole(currentLicenseRole);
        }
        catch (ValidationException ex)
        {
            role = "user";
            permissions = Array.Empty<string>();
            locations = Array.Empty<string>();
            nodeScope = Array.Empty<string>();
            error = ex.Message;
            return false;
        }

        permissions = PermissionsFromPlanJson(currentPlanPermissionsJson);

        if (!TryRefreshLocations(oldLocations, currentAllowedLocations, out locations, out error))
        {
            nodeScope = Array.Empty<string>();
            return false;
        }

        if (!TryRefreshNodeScope(oldNodeScope, allowedNodes, out nodeScope, out error))
            return false;

        error = null;
        return true;
    }

    /// <summary>Legacy overload using plan code for role (tests).</summary>
    public static bool TryComputeRefreshScope(
        string? currentPlan,
        IReadOnlyList<string>? oldLocations,
        IReadOnlyList<string>? currentAllowedLocations,
        IReadOnlyList<string>? oldNodeScope,
        IReadOnlyList<string>? allowedNodes,
        out string role,
        out IReadOnlyList<string> permissions,
        out IReadOnlyList<string> locations,
        out IReadOnlyList<string> nodeScope,
        out string? error) =>
        TryComputeRefreshScope(
            currentLicenseRole: RoleForPlan(currentPlan ?? string.Empty),
            currentPlanPermissionsJson: """["connect"]""",
            oldLocations,
            currentAllowedLocations,
            oldNodeScope,
            allowedNodes,
            out role,
            out permissions,
            out locations,
            out nodeScope,
            out error);

    /// <summary>Stable intersection preserving order of <paramref name="a"/>.</summary>
    public static IReadOnlyList<string> Intersect(IReadOnlyList<string> a, IReadOnlyList<string> b) =>
        IntersectPreserveOrder(Normalize(a), Normalize(b));

    private static List<string> Normalize(IReadOnlyList<string>? values)
    {
        if (values is null || values.Count == 0)
            return new List<string>();

        var seen = new HashSet<string>(StringComparer.Ordinal);
        var list = new List<string>(values.Count);
        foreach (var v in values)
        {
            if (string.IsNullOrWhiteSpace(v))
                continue;
            if (seen.Add(v))
                list.Add(v);
        }

        return list;
    }

    private static List<string> IntersectPreserveOrder(IReadOnlyList<string> a, IReadOnlyList<string> b)
    {
        var set = new HashSet<string>(b, StringComparer.Ordinal);
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var outList = new List<string>();
        foreach (var x in a)
        {
            if (!set.Contains(x) || !seen.Add(x))
                continue;
            outList.Add(x);
        }

        return outList;
    }
}

public sealed class TicketScopeRejectedException : Exception
{
    public TicketScopeRejectedException(string message) : base(message)
    {
    }
}
