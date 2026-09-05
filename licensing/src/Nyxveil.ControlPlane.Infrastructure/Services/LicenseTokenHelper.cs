using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

internal static class LicenseTokenHelper
{
    public static (string PublicId, string Secret) Parse(string licenseToken)
    {
        if (string.IsNullOrWhiteSpace(licenseToken))
            throw new ValidationException("license_token is required");

        var idx = licenseToken.IndexOf(':');
        if (idx <= 0 || idx >= licenseToken.Length - 1)
            throw new ValidationException("license_token must be {licenseId}:{secret}");

        return (licenseToken[..idx], licenseToken[(idx + 1)..]);
    }

    public static async Task<License> LoadUsableAsync(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        string licenseToken,
        CancellationToken cancellationToken)
    {
        var (publicId, secret) = Parse(licenseToken);
        if (!LicenseIdFormat.TryParse(publicId, out var licenseId))
            throw new UnauthorizedException("invalid license token");

        var lic = await db.Licenses
            .Include(l => l.Plan)
            .Include(l => l.AllowedLocations)
            .FirstOrDefaultAsync(l => l.LicenseId == licenseId, cancellationToken)
            .ConfigureAwait(false);

        if (lic is null || !hasher.Verify(lic.LicenseKeyVerifier, secret))
            throw new UnauthorizedException("invalid license token");

        EnsureUsable(lic);
        return lic;
    }

    public static void EnsureUsable(License lic)
    {
        if (lic.Status == LicenseStatus.Revoked)
            throw new ForbiddenException("license revoked");
        if (lic.Status == LicenseStatus.Disabled)
            throw new ForbiddenException("license disabled");
        if (lic.Status == LicenseStatus.Expired ||
            (lic.ExpiresAt is { } exp && exp <= DateTime.UtcNow))
            throw new ForbiddenException("license expired");
        if (lic.Status is not (LicenseStatus.Active or LicenseStatus.Pending))
            throw new ForbiddenException("license not usable");
    }
}
