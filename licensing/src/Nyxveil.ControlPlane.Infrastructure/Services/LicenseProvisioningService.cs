using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Tickets;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class LicenseProvisioningService : ILicenseProvisioningService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;
    private readonly IAuditService _audit;

    public LicenseProvisioningService(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        IClock clock,
        IAuditService audit)
    {
        _db = db;
        _hasher = hasher;
        _clock = clock;
        _audit = audit;
    }

    public async Task<CreateLicenseResponse> CreateLicenseAsync(
        CreateLicenseRequest request,
        CancellationToken cancellationToken = default)
    {
        var plan = await _db.Plans.FirstOrDefaultAsync(p => p.PlanId == request.PlanId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("plan not found");

        var licenseId = LicenseIdFormat.NewLicenseId();
        var publicId = LicenseIdFormat.ToPublicId(licenseId);
        var secret = LicenseIdFormat.ToBase64Url(LicenseIdFormat.GenerateSecretBytes(32));
        var verifier = _hasher.CreateVerifier(secret);
        var now = _clock.UtcNow;

        var license = new License
        {
            LicenseId = licenseId,
            UserId = request.UserId,
            LicenseKeyVerifier = verifier,
            Role = TicketScopeCalculator.NormalizeRole(request.Role),
            PlanId = plan.PlanId,
            Status = LicenseStatus.Active,
            CreatedAt = now,
            ActivatedAt = now,
            ExpiresAt = request.ExpiresAt ?? now.AddDays(plan.DurationDays),
            MaxDevices = request.MaxDevices ?? plan.MaxDevices,
            Note = request.Note,
            ExternalPaymentId = request.ExternalPaymentId,
            CreatedBy = string.IsNullOrWhiteSpace(request.CreatedBy) ? "system" : request.CreatedBy,
            UpdatedAt = now
        };

        var locationInputs = (request.AllowedLocations ?? Array.Empty<string>())
            .Where(x => !string.IsNullOrWhiteSpace(x))
            .Distinct(StringComparer.Ordinal)
            .ToList();
        if (locationInputs.Count > 0)
        {
            var allLocations = await _db.Locations.AsNoTracking().ToListAsync(cancellationToken)
                .ConfigureAwait(false);
            foreach (var loc in locationInputs)
            {
                var canonical = LocationIdResolver.ResolveCanonicalId(allLocations, loc)
                                ?? throw new NotFoundException($"location not found: {loc}");
                license.AllowedLocations.Add(new LicenseAllowedLocation
                {
                    LicenseId = licenseId,
                    LocationId = canonical
                });
            }
        }

        _db.Licenses.Add(license);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = license.CreatedBy,
            Action = "license.create",
            EntityType = "License",
            EntityId = publicId
        }, cancellationToken).ConfigureAwait(false);

        return new CreateLicenseResponse
        {
            LicenseId = publicId,
            LicenseToken = $"{publicId}:{secret}",
            ExpiresAt = license.ExpiresAt
        };
    }

    public async Task ExtendLicenseAsync(ExtendLicenseRequest request, CancellationToken cancellationToken = default)
    {
        var lic = await _db.Licenses.FirstOrDefaultAsync(l => l.LicenseId == request.LicenseId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("license not found");

        lic.ExpiresAt = DateTime.SpecifyKind(request.ExpiresAt.ToUniversalTime(), DateTimeKind.Utc);
        if (lic.Status == LicenseStatus.Expired)
            lic.Status = LicenseStatus.Active;
        lic.UpdatedAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task DisableLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default)
    {
        var lic = await _db.Licenses.FirstOrDefaultAsync(l => l.LicenseId == licenseId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("license not found");
        lic.Status = LicenseStatus.Disabled;
        lic.UpdatedAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task EnableLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default)
    {
        var lic = await _db.Licenses.FirstOrDefaultAsync(l => l.LicenseId == licenseId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("license not found");
        if (lic.Status == LicenseStatus.Revoked)
            throw new ConflictException("cannot enable revoked license");
        lic.Status = LicenseStatus.Active;
        lic.UpdatedAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task RevokeLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default)
    {
        var lic = await _db.Licenses.FirstOrDefaultAsync(l => l.LicenseId == licenseId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("license not found");

        lic.Status = LicenseStatus.Revoked;
        lic.UpdatedAt = _clock.UtcNow;

        var version = await _db.Revocations.MaxAsync(r => (long?)r.Version, cancellationToken).ConfigureAwait(false) ?? 0;
        _db.Revocations.Add(new Revocation
        {
            Id = Guid.NewGuid(),
            Type = RevocationType.License,
            TargetId = LicenseIdFormat.ToPublicId(licenseId),
            CreatedAt = _clock.UtcNow,
            CreatedBy = "system",
            Version = version + 1
        });

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task<LicenseValidateResponse> ValidateLicenseTokenAsync(
        LicenseValidateRequest request,
        CancellationToken cancellationToken = default)
    {
        try
        {
            var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, request.LicenseToken, cancellationToken)
                .ConfigureAwait(false);
            return new LicenseValidateResponse
            {
                Valid = true,
                LicenseId = LicenseIdFormat.ToPublicId(lic.LicenseId),
                Plan = lic.Plan.Code,
                Role = TicketScopeCalculator.NormalizeRole(lic.Role),
                MaxDevices = lic.MaxDevices
            };
        }
        catch (Nyxveil.ControlPlane.Application.Exceptions.ApplicationException ex)
        {
            return new LicenseValidateResponse { Valid = false, Message = ex.Message };
        }
    }
}
