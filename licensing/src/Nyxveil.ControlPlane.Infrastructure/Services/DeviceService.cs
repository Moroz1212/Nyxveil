using System.Data;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class DeviceService : IDeviceService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;

    public DeviceService(ControlPlaneDbContext db, ILicenseKeyHasher hasher, IClock clock)
    {
        _db = db;
        _hasher = hasher;
        _clock = clock;
    }

    public async Task<DeviceActivateResponse> ActivateAsync(
        DeviceActivateRequest request,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(request.DeviceId))
            throw new ValidationException("device_id is required");
        if (request.PublicKey is not { Length: 32 })
            throw new ValidationException("public_key must be 32 bytes Ed25519");

        var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, request.LicenseToken, cancellationToken)
            .ConfigureAwait(false);

        // Serializable (or MSSQL UPDLOCK/HOLDLOCK equivalent) before count+insert to prevent
        // concurrent activations exceeding MaxDevices. InMemory providers approximate this.
        await using var tx = await _db.Database
            .BeginTransactionAsync(IsolationLevel.Serializable, cancellationToken)
            .ConfigureAwait(false);

        if (_db.Database.IsSqlServer())
        {
            // Take key-range / row locks on the license and its device rows before counting.
            await _db.Database.ExecuteSqlInterpolatedAsync(
                    $"""
                     SELECT 1 FROM Licenses WITH (UPDLOCK, HOLDLOCK) WHERE LicenseId = {lic.LicenseId};
                     SELECT 1 FROM Devices WITH (UPDLOCK, HOLDLOCK) WHERE LicenseId = {lic.LicenseId};
                     """,
                    cancellationToken)
                .ConfigureAwait(false);
        }

        var existing = await _db.Devices
            .FirstOrDefaultAsync(
                d => d.LicenseId == lic.LicenseId && d.ClientDeviceId == request.DeviceId,
                cancellationToken)
            .ConfigureAwait(false);

        if (existing is not null)
        {
            if (existing.Status == DeviceStatus.Revoked)
                throw new ForbiddenException("device revoked");

            // Same device_id + same public key is idempotent; key rebind requires remove/admin.
            if (!existing.PublicKey.AsSpan().SequenceEqual(request.PublicKey))
                throw new ConflictException("device public key cannot be rebound");

            existing.Platform = request.Platform ?? existing.Platform;
            existing.DeviceName = request.DeviceName ?? existing.DeviceName;
            existing.Status = DeviceStatus.Active;
            existing.LastSeenAt = _clock.UtcNow;
            existing.RevokedAt = null;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            await tx.CommitAsync(cancellationToken).ConfigureAwait(false);
            return new DeviceActivateResponse { DeviceId = existing.ClientDeviceId, Activated = true };
        }

        var activeCount = await _db.Devices
            .CountAsync(d => d.LicenseId == lic.LicenseId && d.Status != DeviceStatus.Revoked, cancellationToken)
            .ConfigureAwait(false);
        if (activeCount >= lic.MaxDevices)
            throw new ConflictException("max devices reached");

        _db.Devices.Add(new Device
        {
            Id = Guid.NewGuid(),
            ClientDeviceId = request.DeviceId,
            LicenseId = lic.LicenseId,
            PublicKey = request.PublicKey,
            Platform = request.Platform,
            DeviceName = request.DeviceName,
            Status = DeviceStatus.Active,
            CreatedAt = _clock.UtcNow,
            LastSeenAt = _clock.UtcNow
        });

        if (lic.Status == LicenseStatus.Pending)
        {
            lic.Status = LicenseStatus.Active;
            lic.ActivatedAt ??= _clock.UtcNow;
            lic.UpdatedAt = _clock.UtcNow;
        }

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);

        return new DeviceActivateResponse { DeviceId = request.DeviceId, Activated = true };
    }

    public async Task RemoveAsync(string licenseToken, string deviceId, CancellationToken cancellationToken = default)
    {
        var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, licenseToken, cancellationToken)
            .ConfigureAwait(false);
        var device = await _db.Devices
            .FirstOrDefaultAsync(d => d.LicenseId == lic.LicenseId && d.ClientDeviceId == deviceId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("device not found");

        _db.Devices.Remove(device);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task RevokeAsync(string deviceId, CancellationToken cancellationToken = default)
    {
        var devices = await _db.Devices.Where(d => d.ClientDeviceId == deviceId).ToListAsync(cancellationToken)
            .ConfigureAwait(false);
        if (devices.Count == 0)
            throw new NotFoundException("device not found");

        var version = await _db.Revocations.MaxAsync(r => (long?)r.Version, cancellationToken).ConfigureAwait(false) ?? 0;
        foreach (var device in devices)
        {
            device.Status = DeviceStatus.Revoked;
            device.RevokedAt = _clock.UtcNow;
        }

        _db.Revocations.Add(new Revocation
        {
            Id = Guid.NewGuid(),
            Type = RevocationType.Device,
            TargetId = deviceId,
            CreatedAt = _clock.UtcNow,
            CreatedBy = "system",
            Version = version + 1
        });

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task DisableAsync(string deviceId, CancellationToken cancellationToken = default)
    {
        var devices = await _db.Devices.Where(d => d.ClientDeviceId == deviceId).ToListAsync(cancellationToken)
            .ConfigureAwait(false);
        if (devices.Count == 0)
            throw new NotFoundException("device not found");

        foreach (var device in devices)
            device.Status = DeviceStatus.Disabled;

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task<IReadOnlyList<DeviceListItemDto>> ListByLicenseAsync(
        Guid licenseId,
        CancellationToken cancellationToken = default)
    {
        var devices = await _db.Devices.AsNoTracking()
            .Where(d => d.LicenseId == licenseId)
            .OrderBy(d => d.CreatedAt)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var publicId = LicenseIdFormat.ToPublicId(licenseId);
        return devices.Select(d => new DeviceListItemDto
        {
            DeviceId = d.ClientDeviceId,
            LicenseId = publicId,
            Status = d.Status.ToString(),
            Platform = d.Platform,
            DeviceName = d.DeviceName,
            CreatedAt = d.CreatedAt,
            LastSeenAt = d.LastSeenAt
        }).ToList();
    }
}
