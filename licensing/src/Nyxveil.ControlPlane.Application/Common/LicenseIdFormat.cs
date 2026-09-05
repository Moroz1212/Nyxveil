using System.Globalization;
using System.Security.Cryptography;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>
/// Maps DB Guid LicenseId ↔ public Core license id <c>nyx_lic_</c> + 32 hex chars (16 bytes).
/// </summary>
public static class LicenseIdFormat
{
    public const string Prefix = "nyx_lic_";

    public static Guid NewLicenseId() => Guid.NewGuid();

    public static string ToPublicId(Guid licenseId) =>
        Prefix + licenseId.ToString("N", CultureInfo.InvariantCulture);

    public static bool TryParse(string? publicOrRaw, out Guid licenseId)
    {
        licenseId = Guid.Empty;
        if (string.IsNullOrWhiteSpace(publicOrRaw))
            return false;

        var s = publicOrRaw.Trim();
        if (s.StartsWith(Prefix, StringComparison.Ordinal))
            s = s[Prefix.Length..];

        return Guid.TryParseExact(s, "N", out licenseId) || Guid.TryParse(s, out licenseId);
    }

    public static string GenerateSecretHex(int byteCount = 32)
    {
        Span<byte> bytes = stackalloc byte[byteCount];
        RandomNumberGenerator.Fill(bytes);
        return Convert.ToHexString(bytes).ToLowerInvariant();
    }

    public static string ToBase64Url(ReadOnlySpan<byte> data) =>
        Convert.ToBase64String(data).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    public static byte[] GenerateSecretBytes(int byteCount = 32)
    {
        var bytes = new byte[byteCount];
        RandomNumberGenerator.Fill(bytes);
        return bytes;
    }
}
