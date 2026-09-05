using System.IO.Compression;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Portable PBKDF2 + AES-256-GCM backup of signing keys (DPAPI decrypted on export, re-protected on import).
/// </summary>
public sealed class SigningKeyBackupService : ISigningKeyBackupService
{
    private const string EntryName = "signing-keys.portable.json";
    private const string LegacyEntryName = "signing-keys.json.aes";
    private const int SaltSize = 16;
    private const int NonceSize = 12;
    private const int TagSize = 16;
    private const int KeySize = 32;
    private const int Pbkdf2Iterations = PortableSecretCrypto.DefaultIterations;

    private static readonly byte[] Magic = "NVSK1"u8.ToArray();

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly ISigningKeyService _signingKeys;
    private readonly Ed25519SigningKeyStore _keyStore;

    public SigningKeyBackupService(
        IServiceScopeFactory scopeFactory,
        ISigningKeyService signingKeys,
        Ed25519SigningKeyStore keyStore)
    {
        _scopeFactory = scopeFactory;
        _signingKeys = signingKeys;
        _keyStore = keyStore;
    }

    public async Task ExportPortableAsync(
        Stream output,
        string password,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(output);
        ArgumentException.ThrowIfNullOrEmpty(password);

        _ = await _signingKeys.GetCurrentSigningMaterialAsync(cancellationToken).ConfigureAwait(false);

        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var rows = await db.SigningKeysMetadata.AsNoTracking()
            .OrderBy(k => k.CreatedAt)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var bundles = new List<PortableKeyBundle>();
        foreach (var row in rows)
        {
            var raw = _keyStore.UnprotectKeyMaterial(row.ProtectedPrivateKey);
            try
            {
                // Prefer 64-byte seed||public when seed is 32 bytes.
                var exportBytes = NormalizePrivateExport(raw, row.PublicKey);
                var encrypted = PortableSecretCrypto.Encrypt(exportBytes, password);
                CryptographicOperations.ZeroMemory(exportBytes);

                bundles.Add(new PortableKeyBundle
                {
                    FormatVersion = 1,
                    CreatedAt = DateTimeOffset.UtcNow,
                    KeyId = row.KeyId,
                    Algorithm = "Ed25519",
                    Status = row.Status.ToString(),
                    PublicKeyB64 = Convert.ToBase64String(row.PublicKey),
                    SaltB64 = Convert.ToBase64String(encrypted.Salt),
                    Iterations = encrypted.Iterations,
                    NonceB64 = Convert.ToBase64String(encrypted.Nonce),
                    CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
                    TagB64 = Convert.ToBase64String(encrypted.Tag),
                    RetiredAt = row.RetiredAt
                });
            }
            finally
            {
                CryptographicOperations.ZeroMemory(raw);
            }
        }

        var doc = new PortableSigningDocument
        {
            FormatVersion = 1,
            CreatedAt = DateTimeOffset.UtcNow,
            Keys = bundles
        };

        var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(doc));
        await output.WriteAsync(json, cancellationToken).ConfigureAwait(false);
    }

    public async Task ImportPortableAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(input);
        ArgumentException.ThrowIfNullOrEmpty(password);

        using var ms = new MemoryStream();
        await input.CopyToAsync(ms, cancellationToken).ConfigureAwait(false);
        var doc = PortableSecretCrypto.FromJson<PortableSigningDocument>(ms.ToArray());

        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        await using var tx = await db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);

        var hasCurrent = await db.SigningKeysMetadata
            .AnyAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);

        foreach (var key in doc.Keys)
        {
            if (!Enum.TryParse<SigningKeyStatus>(key.Status, ignoreCase: true, out var status))
                throw new InvalidOperationException($"Unknown signing key status '{key.Status}'.");

            var publicKey = Convert.FromBase64String(key.PublicKeyB64);
            var blob = new PortableSecretCrypto.EncryptedBlob(
                Convert.FromBase64String(key.SaltB64),
                key.Iterations,
                Convert.FromBase64String(key.NonceB64),
                Convert.FromBase64String(key.CiphertextB64),
                Convert.FromBase64String(key.TagB64));

            var raw = PortableSecretCrypto.Decrypt(blob, password);
            try
            {
                VerifyPublicMatches(raw, publicKey);

                var seed = ExtractSeed(raw);
                var protectedPrivate = _keyStore.ProtectKeyMaterial(seed);
                CryptographicOperations.ZeroMemory(seed);

                var existing = await db.SigningKeysMetadata
                    .FirstOrDefaultAsync(k => k.KeyId == key.KeyId, cancellationToken)
                    .ConfigureAwait(false);

                if (status == SigningKeyStatus.Current && hasCurrent && existing is null && !force)
                {
                    throw new InvalidOperationException(
                        "A Current signing key already exists. Re-run with --force to overwrite.");
                }

                if (existing is null)
                {
                    if (status == SigningKeyStatus.Current && hasCurrent && !force)
                    {
                        throw new InvalidOperationException(
                            "Refusing to import another Current signing key without --force.");
                    }

                    if (status == SigningKeyStatus.Current && hasCurrent && force)
                    {
                        foreach (var cur in await db.SigningKeysMetadata
                                     .Where(k => k.Status == SigningKeyStatus.Current)
                                     .ToListAsync(cancellationToken)
                                     .ConfigureAwait(false))
                        {
                            cur.Status = SigningKeyStatus.Retired;
                            cur.RetiredAt = DateTime.UtcNow;
                        }
                    }

                    db.SigningKeysMetadata.Add(new SigningKeyMetadata
                    {
                        Id = Guid.NewGuid(),
                        KeyId = key.KeyId,
                        PublicKey = publicKey,
                        ProtectedPrivateKey = protectedPrivate,
                        Status = status,
                        CreatedAt = key.CreatedAt.UtcDateTime,
                        RetiredAt = key.RetiredAt
                    });

                    if (status == SigningKeyStatus.Current)
                        hasCurrent = true;
                }
                else
                {
                    if (existing.Status == SigningKeyStatus.Current && !force &&
                        !existing.ProtectedPrivateKey.AsSpan().SequenceEqual(protectedPrivate))
                    {
                        throw new InvalidOperationException(
                            $"Refusing to overwrite Current key '{key.KeyId}' without --force.");
                    }

                    existing.PublicKey = publicKey;
                    existing.ProtectedPrivateKey = protectedPrivate;
                    existing.Status = status;
                    existing.RetiredAt = key.RetiredAt;
                }
            }
            finally
            {
                CryptographicOperations.ZeroMemory(raw);
            }
        }

        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task ExportEncryptedZipAsync(
        Stream output,
        string password,
        CancellationToken cancellationToken = default)
    {
        using var payload = new MemoryStream();
        await ExportPortableAsync(payload, password, cancellationToken).ConfigureAwait(false);
        payload.Position = 0;

        // Also wrap with legacy AES envelope for older tooling that expects NVSK1 ZIP.
        var encrypted = Encrypt(payload.ToArray(), password);

        using var zip = new ZipArchive(output, ZipArchiveMode.Create, leaveOpen: true);
        var portable = zip.CreateEntry(EntryName, CompressionLevel.Optimal);
        await using (var entryStream = portable.Open())
        {
            payload.Position = 0;
            await payload.CopyToAsync(entryStream, cancellationToken).ConfigureAwait(false);
        }

        var legacy = zip.CreateEntry(LegacyEntryName, CompressionLevel.Optimal);
        await using var legacyStream = legacy.Open();
        await legacyStream.WriteAsync(encrypted, cancellationToken).ConfigureAwait(false);
    }

    public async Task ImportEncryptedZipAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default)
    {
        using var zip = new ZipArchive(input, ZipArchiveMode.Read, leaveOpen: true);

        var portable = zip.GetEntry(EntryName);
        if (portable is not null)
        {
            await using var entryStream = portable.Open();
            await ImportPortableAsync(entryStream, password, force, cancellationToken).ConfigureAwait(false);
            return;
        }

        var legacy = zip.GetEntry(LegacyEntryName)
                     ?? throw new InvalidOperationException($"Backup ZIP is missing '{EntryName}'.");

        await using var legacyStream = legacy.Open();
        using var ms = new MemoryStream();
        await legacyStream.CopyToAsync(ms, cancellationToken).ConfigureAwait(false);
        var json = Decrypt(ms.ToArray(), password);

        // Legacy format stored machine-bound DPAPI blobs — only importable on same machine.
        var doc = JsonSerializer.Deserialize<LegacyBackupDocument>(json)
                  ?? throw new InvalidOperationException("Backup JSON was empty.");

        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        await using var tx = await db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);
        foreach (var key in doc.Keys)
        {
            if (!Enum.TryParse<SigningKeyStatus>(key.Status, ignoreCase: true, out var status))
                throw new InvalidOperationException($"Unknown signing key status '{key.Status}'.");

            var existing = await db.SigningKeysMetadata
                .FirstOrDefaultAsync(k => k.KeyId == key.KeyId, cancellationToken)
                .ConfigureAwait(false);

            if (status == SigningKeyStatus.Current && existing is null)
            {
                var hasCurrent = await db.SigningKeysMetadata
                    .AnyAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
                    .ConfigureAwait(false);
                if (hasCurrent && !force)
                {
                    throw new InvalidOperationException(
                        "A Current signing key already exists. Re-run with --force to overwrite.");
                }
            }

            if (existing is null)
            {
                db.SigningKeysMetadata.Add(new SigningKeyMetadata
                {
                    Id = Guid.NewGuid(),
                    KeyId = key.KeyId,
                    PublicKey = Convert.FromBase64String(key.PublicKeyBase64),
                    ProtectedPrivateKey = Convert.FromBase64String(key.ProtectedPrivateKeyBase64),
                    Status = status,
                    CreatedAt = key.CreatedAt,
                    RetiredAt = key.RetiredAt
                });
            }
            else
            {
                if (existing.Status == SigningKeyStatus.Current && !force)
                {
                    throw new InvalidOperationException(
                        $"Refusing to overwrite Current key '{key.KeyId}' without --force.");
                }

                existing.PublicKey = Convert.FromBase64String(key.PublicKeyBase64);
                existing.ProtectedPrivateKey = Convert.FromBase64String(key.ProtectedPrivateKeyBase64);
                existing.Status = status;
                existing.CreatedAt = key.CreatedAt;
                existing.RetiredAt = key.RetiredAt;
            }
        }

        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
    }

    public static byte[] Encrypt(byte[] plaintext, string password)
    {
        var salt = RandomNumberGenerator.GetBytes(SaltSize);
        var nonce = RandomNumberGenerator.GetBytes(NonceSize);
        var key = DeriveKey(password, salt);
        var ciphertext = new byte[plaintext.Length];
        var tag = new byte[TagSize];
        using (var aes = new AesGcm(key, TagSize))
        {
            aes.Encrypt(nonce, plaintext, ciphertext, tag);
        }

        var result = new byte[Magic.Length + SaltSize + NonceSize + TagSize + ciphertext.Length];
        var offset = 0;
        Magic.CopyTo(result, offset); offset += Magic.Length;
        salt.CopyTo(result, offset); offset += SaltSize;
        nonce.CopyTo(result, offset); offset += NonceSize;
        tag.CopyTo(result, offset); offset += TagSize;
        ciphertext.CopyTo(result, offset);
        CryptographicOperations.ZeroMemory(key);
        return result;
    }

    public static byte[] Decrypt(byte[] blob, string password)
    {
        if (blob.Length < Magic.Length + SaltSize + NonceSize + TagSize)
            throw new CryptographicException("Encrypted backup payload is truncated.");

        if (!blob.AsSpan(0, Magic.Length).SequenceEqual(Magic))
            throw new CryptographicException("Encrypted backup magic mismatch.");

        var offset = Magic.Length;
        var salt = blob.AsSpan(offset, SaltSize).ToArray(); offset += SaltSize;
        var nonce = blob.AsSpan(offset, NonceSize).ToArray(); offset += NonceSize;
        var tag = blob.AsSpan(offset, TagSize).ToArray(); offset += TagSize;
        var ciphertext = blob.AsSpan(offset).ToArray();

        var key = DeriveKey(password, salt);
        var plaintext = new byte[ciphertext.Length];
        try
        {
            using var aes = new AesGcm(key, TagSize);
            aes.Decrypt(nonce, ciphertext, tag, plaintext);
            return plaintext;
        }
        finally
        {
            CryptographicOperations.ZeroMemory(key);
        }
    }

    private static byte[] DeriveKey(string password, byte[] salt)
    {
        return Rfc2898DeriveBytes.Pbkdf2(
            Encoding.UTF8.GetBytes(password),
            salt,
            Pbkdf2Iterations,
            HashAlgorithmName.SHA256,
            KeySize);
    }

    private static byte[] NormalizePrivateExport(byte[] raw, byte[] publicKey)
    {
        if (raw.Length == 64)
            return raw.ToArray();

        if (raw.Length == 32 && publicKey.Length == 32)
        {
            var combined = new byte[64];
            raw.CopyTo(combined, 0);
            publicKey.CopyTo(combined, 32);
            return combined;
        }

        return raw.ToArray();
    }

    private static byte[] ExtractSeed(byte[] raw)
    {
        if (raw.Length >= 32)
            return raw.AsSpan(0, 32).ToArray();
        throw new CryptographicException("Decrypted signing key material is too short.");
    }

    private static void VerifyPublicMatches(byte[] raw, byte[] expectedPublic)
    {
        var seed = ExtractSeed(raw);
        try
        {
            using var key = Key.Import(SignatureAlgorithm.Ed25519, seed, KeyBlobFormat.RawPrivateKey);
            var actual = key.PublicKey.Export(KeyBlobFormat.RawPublicKey);
            if (!actual.AsSpan().SequenceEqual(expectedPublic))
                throw new CryptographicException("Decrypted private key does not match public_key_b64.");
        }
        finally
        {
            CryptographicOperations.ZeroMemory(seed);
        }
    }

    private sealed class PortableSigningDocument
    {
        public int FormatVersion { get; set; } = 1;
        public DateTimeOffset CreatedAt { get; set; }
        public List<PortableKeyBundle> Keys { get; set; } = new();
    }

    private sealed class LegacyBackupDocument
    {
        public List<LegacyBackupKey> Keys { get; set; } = new();
    }

    private sealed class LegacyBackupKey
    {
        public string KeyId { get; set; } = string.Empty;
        public string PublicKeyBase64 { get; set; } = string.Empty;
        public string ProtectedPrivateKeyBase64 { get; set; } = string.Empty;
        public string Status { get; set; } = string.Empty;
        public DateTime CreatedAt { get; set; }
        public DateTime? RetiredAt { get; set; }
    }
}
