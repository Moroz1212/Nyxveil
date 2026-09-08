using System.Globalization;
using System.Text.RegularExpressions;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>Minimal SemVer 2.0 parser/comparer for X.Y.Z[-pre][+build]. Not System.Version.</summary>
public readonly partial struct SemVersion : IComparable<SemVersion>, IEquatable<SemVersion>
{
    private static readonly Regex Pattern = SemVerRegex();

    public int Major { get; }
    public int Minor { get; }
    public int Patch { get; }
    public string? PreRelease { get; }
    public string? Build { get; }
    public string Original { get; }

    public bool IsPrerelease => !string.IsNullOrEmpty(PreRelease);

    private SemVersion(int major, int minor, int patch, string? pre, string? build, string original)
    {
        Major = major;
        Minor = minor;
        Patch = patch;
        PreRelease = pre;
        Build = build;
        Original = original;
    }

    public static bool TryParse(string? input, out SemVersion version)
    {
        version = default;
        if (string.IsNullOrWhiteSpace(input))
            return false;

        var raw = input.Trim();
        if (raw.StartsWith('v') || raw.StartsWith('V'))
            raw = raw[1..];

        var m = Pattern.Match(raw);
        if (!m.Success)
            return false;

        version = new SemVersion(
            int.Parse(m.Groups[1].Value, CultureInfo.InvariantCulture),
            int.Parse(m.Groups[2].Value, CultureInfo.InvariantCulture),
            int.Parse(m.Groups[3].Value, CultureInfo.InvariantCulture),
            m.Groups[4].Success ? m.Groups[4].Value : null,
            m.Groups[5].Success ? m.Groups[5].Value : null,
            input.Trim());
        return true;
    }

    public static SemVersion Parse(string input) =>
        TryParse(input, out var v) ? v : throw new FormatException("Invalid SemVer: " + input);

    public int CompareTo(SemVersion other)
    {
        var c = Major.CompareTo(other.Major);
        if (c != 0) return c;
        c = Minor.CompareTo(other.Minor);
        if (c != 0) return c;
        c = Patch.CompareTo(other.Patch);
        if (c != 0) return c;

        // No prerelease > any prerelease
        if (PreRelease is null && other.PreRelease is null) return 0;
        if (PreRelease is null) return 1;
        if (other.PreRelease is null) return -1;
        return string.CompareOrdinal(PreRelease, other.PreRelease);
    }

    public bool Equals(SemVersion other) => CompareTo(other) == 0;
    public override bool Equals(object? obj) => obj is SemVersion s && Equals(s);
    public override int GetHashCode() => HashCode.Combine(Major, Minor, Patch, PreRelease);
    public override string ToString() =>
        PreRelease is null ? $"{Major}.{Minor}.{Patch}" : $"{Major}.{Minor}.{Patch}-{PreRelease}";

    public static bool operator <(SemVersion a, SemVersion b) => a.CompareTo(b) < 0;
    public static bool operator >(SemVersion a, SemVersion b) => a.CompareTo(b) > 0;
    public static bool operator <=(SemVersion a, SemVersion b) => a.CompareTo(b) <= 0;
    public static bool operator >=(SemVersion a, SemVersion b) => a.CompareTo(b) >= 0;

    [GeneratedRegex(@"^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$", RegexOptions.CultureInvariant)]
    private static partial Regex SemVerRegex();
}
