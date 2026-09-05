using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Shared PBKDF2-HMAC-SHA256 + AES-256-GCM portable secret crypto for recovery bundles.
/// </summary>
public static class PortableSecretCrypto
{
    public const int DefaultIterations = 210_000;
    public const int SaltSize = 16;
    public const int NonceSize = 12;
    public const int TagSize = 16;
    public const int KeySize = 32;

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        WriteIndented = true
    };

    public static EncryptedBlob Encrypt(byte[] plaintext, string password, int iterations = DefaultIterations)
    {
        ArgumentNullException.ThrowIfNull(plaintext);
        ArgumentException.ThrowIfNullOrEmpty(password);

        var salt = RandomNumberGenerator.GetBytes(SaltSize);
        var nonce = RandomNumberGenerator.GetBytes(NonceSize);
        var key = DeriveKey(password, salt, iterations);
        var ciphertext = new byte[plaintext.Length];
        var tag = new byte[TagSize];
        try
        {
            using var aes = new AesGcm(key, TagSize);
            aes.Encrypt(nonce, plaintext, ciphertext, tag);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(key);
        }

        return new EncryptedBlob(salt, iterations, nonce, ciphertext, tag);
    }

    public static byte[] Decrypt(EncryptedBlob blob, string password)
    {
        ArgumentNullException.ThrowIfNull(blob);
        ArgumentException.ThrowIfNullOrEmpty(password);

        var key = DeriveKey(password, blob.Salt, blob.Iterations);
        var plaintext = new byte[blob.Ciphertext.Length];
        try
        {
            using var aes = new AesGcm(key, TagSize);
            aes.Decrypt(blob.Nonce, blob.Ciphertext, blob.Tag, plaintext);
            return plaintext;
        }
        finally
        {
            CryptographicOperations.ZeroMemory(key);
        }
    }

    public static byte[] DeriveKey(string password, byte[] salt, int iterations)
    {
        return Rfc2898DeriveBytes.Pbkdf2(
            Encoding.UTF8.GetBytes(password),
            salt,
            iterations,
            HashAlgorithmName.SHA256,
            KeySize);
    }

    public static string ToJson<T>(T value) => JsonSerializer.Serialize(value, JsonOptions);

    public static T FromJson<T>(ReadOnlySpan<byte> utf8) =>
        JsonSerializer.Deserialize<T>(utf8, JsonOptions)
        ?? throw new InvalidOperationException("JSON payload was empty.");

    public static T FromJson<T>(string json) =>
        JsonSerializer.Deserialize<T>(json, JsonOptions)
        ?? throw new InvalidOperationException("JSON payload was empty.");

    public sealed record EncryptedBlob(
        byte[] Salt,
        int Iterations,
        byte[] Nonce,
        byte[] Ciphertext,
        byte[] Tag);
}

/// <summary>One portable encrypted key entry (signing private material).</summary>
public sealed class PortableKeyBundle
{
    public int FormatVersion { get; set; } = 1;
    public DateTimeOffset CreatedAt { get; set; }
    public string KeyId { get; set; } = string.Empty;
    public string Algorithm { get; set; } = "Ed25519";
    public string Status { get; set; } = string.Empty;
    public string PublicKeyB64 { get; set; } = string.Empty;
    public string SaltB64 { get; set; } = string.Empty;
    public int Iterations { get; set; }
    public string NonceB64 { get; set; } = string.Empty;
    public string CiphertextB64 { get; set; } = string.Empty;
    public string TagB64 { get; set; } = string.Empty;
    public DateTime? RetiredAt { get; set; }
}

/// <summary>Portable License KEK bundle.</summary>
public sealed class PortableKekBundle
{
    public int FormatVersion { get; set; } = 1;
    public DateTimeOffset CreatedAt { get; set; }
    public string Algorithm { get; set; } = "LicenseKekHex";
    public string SaltB64 { get; set; } = string.Empty;
    public int Iterations { get; set; }
    public string NonceB64 { get; set; } = string.Empty;
    public string CiphertextB64 { get; set; } = string.Empty;
    public string TagB64 { get; set; } = string.Empty;
}

/// <summary>
/// Unified recovery document: signing keys + license_kek. Never contains SQL password.
/// </summary>
public sealed class ControlPlaneRecoveryBundle
{
    public int FormatVersion { get; set; } = 1;
    public DateTimeOffset CreatedAt { get; set; }
    public List<PortableKeyBundle> SigningKeys { get; set; } = new();
    public PortableKekBundle? LicenseKek { get; set; }
}
