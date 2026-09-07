namespace Nyxveil.Client.Core;

/// <summary>
/// Catalog timestamps are absolute UTC instants. Unspecified Kind means UTC wall
/// (Control Plane / Frozen Core contract) — never host-local conversion.
/// </summary>
public static class CatalogTime
{
    /// <summary>Matches AccessTicketService MaxIssuedAtSkewSeconds / Go ticket maxIssuedAtSkew.</summary>
    public static readonly TimeSpan MaxIssuedAtSkew = TimeSpan.FromMinutes(5);

    public static DateTime NormalizeUtcWall(DateTime value) => value.Kind switch
    {
        DateTimeKind.Utc => value,
        DateTimeKind.Local => DateTime.SpecifyKind(value.ToUniversalTime(), DateTimeKind.Utc),
        _ => DateTime.SpecifyKind(value, DateTimeKind.Utc)
    };

    public static DateTimeOffset AsUtcInstant(DateTime value) =>
        new(NormalizeUtcWall(value), TimeSpan.Zero);

    /// <summary>
    /// Temporal window: issued_at − skew ≤ now ≤ expires_at.
    /// Skew only relaxes not-before (clock behind / CP IssuedAt slightly ahead);
    /// expires_at stays fail-closed (no extension).
    /// </summary>
    public static bool IsTemporallyValid(DateTimeOffset nowUtc, DateTimeOffset issuedAt, DateTimeOffset expiresAt,
        TimeSpan? issuedAtSkew = null)
    {
        var skew = issuedAtSkew ?? MaxIssuedAtSkew;
        if (nowUtc > expiresAt)
            return false;
        if (nowUtc < issuedAt - skew)
            return false;
        return true;
    }
}
