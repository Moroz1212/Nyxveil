using System.Security.Cryptography;
using System.Text;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// HMAC-SHA256 license/bootstrap secret verifiers matching Go store/secret.go (hmac1: prefix).
/// </summary>
public sealed class LicenseKeyHasher : ILicenseKeyHasher
{
    public const string HmacPrefix = "hmac1:";
    public const string LegacyEncPrefix = "nvp1:";
    private const int KekSize = 32;

    private readonly byte[] _kek;

    public LicenseKeyHasher(IOptions<SecurityOptions> options)
    {
        _kek = ParseKek(options.Value.LicenseKekHex)
               ?? throw new InvalidOperationException(
                   "Security:LicenseKekHex must be 64 hex characters (32 bytes) for HMAC verifiers.");
    }

    public string CreateVerifier(string secret)
    {
        if (string.IsNullOrEmpty(secret))
            return string.Empty;

        var mac = HMACSHA256.HashData(_kek, Encoding.UTF8.GetBytes(secret));
        return HmacPrefix + Convert.ToHexString(mac).ToLowerInvariant();
    }

    public bool Verify(string stored, string candidate)
    {
        if (string.IsNullOrEmpty(stored) || string.IsNullOrEmpty(candidate))
            return false;

        if (stored.StartsWith(HmacPrefix, StringComparison.Ordinal))
        {
            byte[] want;
            try
            {
                want = Convert.FromHexString(stored[HmacPrefix.Length..]);
            }
            catch (FormatException)
            {
                return false;
            }

            var got = HMACSHA256.HashData(_kek, Encoding.UTF8.GetBytes(candidate));
            return CryptographicOperations.FixedTimeEquals(want, got);
        }

        if (stored.StartsWith(LegacyEncPrefix, StringComparison.Ordinal))
        {
            // Legacy ChaCha20-Poly1305 ciphertext is not decryptable without full AEAD path;
            // production Control Plane stores hmac1 only. Reject legacy ciphertext here.
            return false;
        }

        var a = Encoding.UTF8.GetBytes(stored);
        var b = Encoding.UTF8.GetBytes(candidate);
        return a.Length == b.Length && CryptographicOperations.FixedTimeEquals(a, b);
    }

    public static byte[]? ParseKek(string? raw)
    {
        if (string.IsNullOrWhiteSpace(raw))
            return null;

        raw = raw.Trim();
        if (raw.Length == 64)
        {
            try
            {
                var b = Convert.FromHexString(raw);
                if (b.Length == KekSize)
                    return b;
            }
            catch (FormatException)
            {
                return null;
            }
        }

        if (raw.Length == KekSize)
            return Encoding.UTF8.GetBytes(raw);

        return null;
    }
}
