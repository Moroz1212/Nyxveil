using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class BootstrapTokenService : IBootstrapTokenService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;

    public BootstrapTokenService(ControlPlaneDbContext db, ILicenseKeyHasher hasher, IClock clock)
    {
        _db = db;
        _hasher = hasher;
        _clock = clock;
    }

    public async Task<CreateBootstrapTokenResponse> CreateAsync(
        CreateBootstrapTokenRequest request,
        CancellationToken cancellationToken = default)
    {
        if (request.MaxUses < 1)
            throw new ValidationException("max_uses must be >= 1");
        if (request.ExpiresAt <= _clock.UtcNow)
            throw new ValidationException("expires_at must be in the future");

        var raw = "nvp_boot_" + LicenseIdFormat.GenerateSecretHex(16);
        var entity = new BootstrapToken
        {
            BootstrapId = Guid.NewGuid(),
            Verifier = _hasher.CreateVerifier(raw),
            ExpiresAt = DateTime.SpecifyKind(request.ExpiresAt.ToUniversalTime(), DateTimeKind.Utc),
            MaxUses = request.MaxUses,
            UsedCount = 0,
            AllowedLocation = request.AllowedLocation,
            Status = BootstrapTokenStatus.Active,
            CreatedAt = _clock.UtcNow,
            CreatedBy = string.IsNullOrWhiteSpace(request.CreatedBy) ? "system" : request.CreatedBy,
            Note = request.Note
        };

        _db.BootstrapTokens.Add(entity);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        return new CreateBootstrapTokenResponse
        {
            BootstrapId = entity.BootstrapId,
            BootstrapToken = raw,
            ExpiresAt = entity.ExpiresAt,
            MaxUses = entity.MaxUses
        };
    }

    public async Task<IReadOnlyList<BootstrapTokenListItemDto>> ListAsync(CancellationToken cancellationToken = default)
    {
        var tokens = await _db.BootstrapTokens.AsNoTracking()
            .OrderByDescending(t => t.CreatedAt)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        return tokens.Select(t => new BootstrapTokenListItemDto
        {
            BootstrapId = t.BootstrapId,
            ExpiresAt = t.ExpiresAt,
            MaxUses = t.MaxUses,
            UsedCount = t.UsedCount,
            AllowedLocation = t.AllowedLocation,
            Status = t.Status.ToString(),
            CreatedAt = t.CreatedAt,
            CreatedBy = t.CreatedBy,
            Note = t.Note
        }).ToList();
    }
}
