using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class RevocationService : IRevocationService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;

    public RevocationService(ControlPlaneDbContext db, IClock clock)
    {
        _db = db;
        _clock = clock;
    }

    public async Task<RevocationListResponse> GetSnapshotForNodeAsync(
        string nodeId,
        CancellationToken cancellationToken = default)
    {
        _ = nodeId;
        var rows = await _db.Revocations.AsNoTracking()
            .OrderByDescending(r => r.Version)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var resp = new RevocationListResponse
        {
            UpdatedAt = new DateTimeOffset(_clock.UtcNow).ToUnixTimeSeconds()
        };

        foreach (var r in rows)
        {
            switch (r.Type)
            {
                case RevocationType.Ticket:
                    resp.RevokedJtis.Add(r.TargetId);
                    break;
                case RevocationType.License:
                    resp.RevokedLicenses.Add(r.TargetId);
                    break;
                case RevocationType.Device:
                    resp.RevokedDevices.Add(r.TargetId);
                    break;
            }
        }

        return resp;
    }

    public Task RevokeTicketAsync(string jti, CancellationToken cancellationToken = default) =>
        AddAsync(RevocationType.Ticket, jti, cancellationToken);

    public async Task RevokeLicenseAsync(string licenseId, CancellationToken cancellationToken = default)
    {
        if (LicenseIdFormat.TryParse(licenseId, out var id))
        {
            var lic = await _db.Licenses.FirstOrDefaultAsync(l => l.LicenseId == id, cancellationToken)
                .ConfigureAwait(false);
            if (lic is not null)
            {
                lic.Status = LicenseStatus.Revoked;
                lic.UpdatedAt = _clock.UtcNow;
            }
        }

        await AddAsync(RevocationType.License, licenseId, cancellationToken).ConfigureAwait(false);
    }

    public async Task RevokeDeviceAsync(string deviceId, CancellationToken cancellationToken = default)
    {
        var devices = await _db.Devices.Where(d => d.ClientDeviceId == deviceId).ToListAsync(cancellationToken)
            .ConfigureAwait(false);
        foreach (var d in devices)
        {
            d.Status = DeviceStatus.Revoked;
            d.RevokedAt = _clock.UtcNow;
        }

        await AddAsync(RevocationType.Device, deviceId, cancellationToken).ConfigureAwait(false);
    }

    private async Task AddAsync(RevocationType type, string targetId, CancellationToken cancellationToken)
    {
        var version = await _db.Revocations.MaxAsync(r => (long?)r.Version, cancellationToken).ConfigureAwait(false) ?? 0;
        _db.Revocations.Add(new Revocation
        {
            Id = Guid.NewGuid(),
            Type = type,
            TargetId = targetId,
            CreatedAt = _clock.UtcNow,
            CreatedBy = "system",
            Version = version + 1
        });
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }
}
