using Nyxveil.ControlPlane.Domain.Entities;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>
/// Resolves admin aliases (<see cref="Location.Code"/>) to canonical <see cref="Location.LocationId"/>.
/// Security scope always uses LocationId.
/// </summary>
public static class LocationIdResolver
{
    public static string? ResolveCanonicalId(IEnumerable<Location> locations, string? input)
    {
        if (string.IsNullOrWhiteSpace(input))
            return null;

        var value = input.Trim();
        foreach (var loc in locations)
        {
            if (string.Equals(loc.LocationId, value, StringComparison.Ordinal))
                return loc.LocationId;
        }

        foreach (var loc in locations)
        {
            if (string.Equals(loc.Code, value, StringComparison.Ordinal))
                return loc.LocationId;
        }

        return null;
    }
}
