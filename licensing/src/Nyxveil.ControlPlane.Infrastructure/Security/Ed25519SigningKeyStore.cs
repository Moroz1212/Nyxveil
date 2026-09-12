using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using NSec.Cryptography;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.Extensions.DependencyInjection;
using ProtectedData = System.Security.Cryptography.ProtectedData;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Ed25519 signing keys with Current / Next / Retiring / Retired lifecycle.
/// Verification ring includes Retiring until RetireAfter.
/// </summary>
public sealed class Ed25519SigningKeyStore : ISigningKeyService
{
    public const string DataProtectionPurpose = "nvp-signing-key";

    private readonly IServiceScopeFactory _scopeFactory;
    private readonly IDataProtector _protector;
    private readonly IClock _clock;
    private readonly SigningKeyRotationOptions _rotation;
    private readonly TicketOptions _tickets;
    private readonly ICriticalOperationAuthorizer _criticalOps;

    public Ed25519SigningKeyStore(
        IServiceScopeFactory scopeFactory,
        IDataProtectionProvider dataProtection,
        IClock clock,
        IOptions<SigningKeyRotationOptions>? rotation = null,
        IOptions<TicketOptions>? tickets = null,
        ICriticalOperationAuthorizer? criticalOps = null)
    {
        _scopeFactory = scopeFactory;
        _protector = dataProtection.CreateProtector(DataProtectionPurpose);
        _clock = clock;
        _rotation = rotation?.Value ?? new SigningKeyRotationOptions();
        _tickets = tickets?.Value ?? new TicketOptions();
        _criticalOps = criticalOps ?? AllowAllCriticalOperationAuthorizer.Instance;
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
        _criticalOps.AssertAllowed(CriticalOperation.SigningKeyMutate);
        _rotation.EnsureGraceSafe(TimeSpan.FromMinutes(Math.Max(1, _tickets.TtlMinutes)));

        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        await using var tx = await db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);

        await FinalizeExpiredRetiringCoreAsync(db, cancellationToken).ConfigureAwait(false);

        var now = _clock.UtcNow;
        var retiring = await db.SigningKeysMetadata
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Retiring, cancellationToken)
            .ConfigureAwait(false);
        if (retiring is not null)
            throw new ConflictException("rotation blocked: an active Retiring key exists until " +
                                        (retiring.RetireAfter?.ToString("O") ?? "unknown"));

        var current = await db.SigningKeysMetadata
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Current, cancellationToken)
            .ConfigureAwait(false);
        var next = await db.SigningKeysMetadata
            .FirstOrDefaultAsync(k => k.Status == SigningKeyStatus.Next, cancellationToken)
            .ConfigureAwait(false);

        if (next is null)
        {
            db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Next, now));
            await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
            throw new ValidationException(
                "Следующий ключ только что подготовлен. Ротация станет доступна после периода предварительного распространения.");
        }

        if (next.CreatedAt.Add(_rotation.NextPrepublishPeriod) > now)
        {
            var readyAt = next.CreatedAt.Add(_rotation.NextPrepublishPeriod);
            throw new ValidationException(
                $"Следующий ключ ещё не готов к ротации. Повторите после {readyAt:dd.MM.yyyy HH:mm:ss} UTC.");
        }

        var previousKeyId = current?.KeyId ?? string.Empty;

        if (current is not null)
        {
            current.Status = SigningKeyStatus.Retiring;
            current.RetireAfter = now.Add(_rotation.RetiringGracePeriod);
            current.RetiredAt = null;
        }

        next.Status = SigningKeyStatus.Current;
        next.PromotedAt = now;
        var newKeyId = next.KeyId;

        db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Next, now));
        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);

        return new RotateSigningKeyResult
        {
            NewKeyId = newKeyId,
            PreviousKeyId = previousKeyId
        };
    }

    public async Task<IReadOnlyList<VerificationKeyDto>> GetVerificationKeysAsync(
        CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        await EnsureKeysAsync(db, cancellationToken).ConfigureAwait(false);
        await FinalizeExpiredRetiringCoreAsync(db, cancellationToken).ConfigureAwait(false);

        var now = _clock.UtcNow;
        var keys = await db.SigningKeysMetadata
            .AsNoTracking()
            .Where(k =>
                k.Status == SigningKeyStatus.Current ||
                k.Status == SigningKeyStatus.Next ||
                (k.Status == SigningKeyStatus.Retiring &&
                 (k.RetireAfter == null || k.RetireAfter > now)))
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        return keys.Select(k => new VerificationKeyDto
        {
            KeyId = k.KeyId,
            PublicKey = k.PublicKey,
            Status = k.Status.ToString()
        }).ToList();
    }

    public async Task<IReadOnlyList<SigningKeyAdminDto>> ListAllKeysForAdminAsync(
        CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        await FinalizeExpiredRetiringCoreAsync(db, cancellationToken).ConfigureAwait(false);

        var keys = await db.SigningKeysMetadata.AsNoTracking()
            .OrderBy(k => k.Status)
            .ThenByDescending(k => k.CreatedAt)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        return keys.Select(k => new SigningKeyAdminDto
        {
            KeyId = k.KeyId,
            PublicKey = k.PublicKey,
            Status = k.Status.ToString(),
            CreatedAt = k.CreatedAt,
            PromotedAt = k.PromotedAt,
            RetireAfter = k.RetireAfter,
            RetiredAt = k.RetiredAt
        }).ToList();
    }

    public async Task<int> FinalizeExpiredRetiringAsync(CancellationToken cancellationToken = default)
    {
        using var scope = _scopeFactory.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        return await FinalizeExpiredRetiringCoreAsync(db, cancellationToken).ConfigureAwait(false);
    }

    private async Task<int> FinalizeExpiredRetiringCoreAsync(
        ControlPlaneDbContext db,
        CancellationToken cancellationToken)
    {
        var now = _clock.UtcNow;
        var expired = await db.SigningKeysMetadata
            .Where(k => k.Status == SigningKeyStatus.Retiring &&
                        k.RetireAfter != null &&
                        k.RetireAfter <= now)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        if (expired.Count == 0)
            return 0;

        foreach (var k in expired)
        {
            k.Status = SigningKeyStatus.Retired;
            k.RetiredAt = now;
        }

        await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        return expired.Count;
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
            var now = _clock.UtcNow;
            var current = CreateKeyEntity(SigningKeyStatus.Current, now);
            current.PromotedAt = now;
            db.SigningKeysMetadata.Add(current);
            db.SigningKeysMetadata.Add(CreateKeyEntity(SigningKeyStatus.Next, now));
            await db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        }

        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
    }

    private SigningKeyMetadata CreateKeyEntity(SigningKeyStatus status, DateTime createdAt)
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
            CreatedAt = createdAt
        };
    }

    public byte[] ProtectKeyMaterial(byte[] plaintext) => Protect(plaintext);
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
