using System.Runtime.InteropServices;
using System.Security.Cryptography;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using ProtectedData = System.Security.Cryptography.ProtectedData;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Ed25519 signing keys: generate via NSec, protect with Windows DPAPI (LocalMachine) or ASP.NET DataProtection,
/// persist metadata in SigningKeysMetadata. Supports Current + Next.
/// </summary>
public sealed class Ed25519SigningKeyStore : ISigningKeyService
{
    public const string DataProtectionPurpose = "nvp-signing-key";

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly IDataProtector _protector;

    public Ed25519SigningKeyStore(IServiceScopeFactory scopeFactory, IDataProtectionProvider dataProtection)
    {
        _scopeFactory = scopeFactory;
        _protector = dataProtection.CreateProtector(DataProtectionPurpose);
    }

    public async Task<SigningMaterialDto> GetCurrentSigningMaterialAsync(CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        var current = await db.SigningKeysMetadata
            .AsNoTracking()
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);

        if (current is null)
        {
            await EnsureKeysAsync(db, cancellationToken).ConfigureAwait(false);
            current = await db.SigningKeysMetadata
                .AsNoTracking()
                .FirstAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
                .ConfigureAwait(false);
        }

        return new SigningMaterialDto
        {
            KeyId = current.KeyId,
            PublicKey = current.PublicKey,
            PrivateKey = Unprotect(current.ProtectedPrivateKey)
        };
    }

    public async Task<RotateSigningKeyResult> RotateAsync(CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        await using var tx = await db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);

        var current = await db.SigningKeysMetadata
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);
        var next = await db.SigningKeysMetadata
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Next, cancellationToken)
            .ConfigureAwait(false);

        var previousKeyId = current?.KeyId ?? string.Empty;

        if (current is not null)
        {
            current.Status = SigningKeyStatus.Retired;
            current.RetiredAt = DateTime.UtcNow;
        }

        string newKeyId;
        if (next is not null)
        {
            next.Status = SigningKeyStatus.Current;
            newKeyId = next.KeyId;
        }
        else
        {
            var created = CreateKeyEntity(SigningKeyStatus.Current);
            db.SigningKeysMetadata.Add(created);
            newKeyId = created.KeyId;
        }

        db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Next));
        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);

        return new RotateSigningKeyResult
        {
            NewKeyId = newKeyId,
            PreviousKeyId = previousKeyId
        };
    }

    public async Task<IReadOnlyList<VerificationKeyDto>> GetVerificationKeysAsync(CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        await EnsureKeysAsync(db, cancellationToken).ConfigureAwait(false);

        var keys = await db.SigningKeysMetadata
            .AsNoTracking()
            .Where(k => k.Status == SigningKeyStatus.Current || k.Status == SigningKeyStatus.Next)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        return keys.Select(k => new VerificationKeyDto
        {
            KeyId = k.KeyId,
            PublicKey = k.PublicKey,
            Status = k.Status.ToString()
        }).ToList();
    }

    public static Key ImportSigningKey(byte[] seedOrPkcs8)
    {
        var algo = SignatureAlgorithm.Ed25519;
        if (seedOrPkcs8.Length == 32)
            return Key.Import(algo, seedOrPkcs8, KeyBlobFormat.RawPrivateKey);
        if (seedOrPkcs8.Length == 64)
            return Key.Import(algo, seedOrPkcs8.AsSpan(0, 32), KeyBlobFormat.RawPrivateKey);

        return Key.Import(algo, seedOrPkcs8, KeyBlobFormat.PkixPrivateKey);
    }

    public static byte[] Sign(byte[] seedOrPkcs8, ReadOnlySpan<byte> data)
    {
        using var key = ImportSigningKey(seedOrPkcs8);
        return SignatureAlgorithm.Ed25519.Sign(key, data);
    }

    public static bool Verify(byte[] publicKey, ReadOnlySpan<byte> data, ReadOnlySpan<byte> signature)
    {
        var pub = PublicKey.Import(SignatureAlgorithm.Ed25519, publicKey, KeyBlobFormat.RawPublicKey);
        return SignatureAlgorithm.Ed25519.Verify(pub, data, signature);
    }

    private async Task EnsureKeysAsync(ControlPlaneDbContext db, CancellationToken cancellationToken)
    {
        var hasCurrent = await db.SigningKeysMetadata
            .AnyAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);
        if (hasCurrent)
            return;

        await using var tx = await db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);
        hasCurrent = await db.SigningKeysMetadata
            .AnyAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);
        if (!hasCurrent)
        {
            db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Current));
            db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Next));
            await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        }

        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
    }

    private SigningKeyMetadata CreateKeyEntity(SigningKeyStatus status)
    {
        using var key = Key.Create(
            SignatureAlgorithm.Ed25519,
            new KeyCreationParameters { ExportPolicy = KeyExportPolicies.AllowPlaintextExport });
        var seed = key.Export(KeyBlobFormat.RawPrivateKey);
        var pub = key.PublicKey.Export(KeyBlobFormat.RawPublicKey);
        var keyId = "cp-key-" + Convert.ToHexString(RandomNumberGenerator.GetBytes(8)).ToLowerInvariant();

        return new SigningKeyMetadata
        {
            Id = Guid.NewGuid(),
            KeyId = keyId,
            PublicKey = pub,
            ProtectedPrivateKey = Protect(seed),
            Status = status,
            CreatedAt = DateTime.UtcNow
        };
    }

    /// <summary>Protect raw Ed25519 seed for this machine (DPAPI LocalMachine or DataProtection).</summary>
    public byte[] ProtectKeyMaterial(byte[] plaintext) => Protect(plaintext);

    /// <summary>Unprotect stored signing key material for portable export / signing.</summary>
    public byte[] UnprotectKeyMaterial(byte[] protectedBytes) => Unprotect(protectedBytes);

    private byte[] Protect(byte[] plaintext)
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
            return ProtectedData.Protect(plaintext, optionalEntropy: null, DataProtectionScope.LocalMachine);

        return _protector.Protect(plaintext);
    }

    private byte[] Unprotect(byte[] protectedBytes)
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
        {
            try
            {
                return ProtectedData.Unprotect(protectedBytes, optionalEntropy: null, DataProtectionScope.LocalMachine);
            }
            catch (CryptographicException)
            {
            }
        }

        return _protector.Unprotect(protectedBytes);
    }
}
