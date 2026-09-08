using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class InfrastructureOverviewService : IInfrastructureOverviewService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;
    private readonly IControlPlaneCertificateStatusService _cpCerts;
    private readonly CertificateExpiryOptions _expiry;

    public InfrastructureOverviewService(
        ControlPlaneDbContext db,
        IClock clock,
        IControlPlaneCertificateStatusService cpCerts,
        IOptions<CertificateExpiryOptions>? expiry = null)
    {
        _db = db;
        _clock = clock;
        _cpCerts = cpCerts;
        _expiry = expiry?.Value ?? new CertificateExpiryOptions();
    }

    public async Task<InfrastructureOverviewDto> GetOverviewAsync(CancellationToken cancellationToken = default)
    {
        var now = _clock.UtcNow;
        var cp = await _cpCerts.GetStatusAsync(cancellationToken).ConfigureAwait(false);

        var nodes = await _db.Nodes.AsNoTracking()
            .Where(n => n.LifecycleState == NodeLifecycleState.Active)
            .OrderBy(n => n.DisplayName)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var recent = await _db.NodeCommands.AsNoTracking()
            .OrderByDescending(c => c.CreatedAt)
            .Take(25)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        return new InfrastructureOverviewDto
        {
            ControlPlaneCertificate = cp,
            Nodes = nodes.Select(n => new NodeCertificateHealthDto
            {
                NodeId = n.NodeId,
                DisplayName = n.DisplayName,
                CertThumbprint = n.CertThumbprint,
                CertNotAfter = n.CertNotAfter,
                DaysRemaining = CertificateExpiry.DaysRemaining(n.CertNotAfter, now),
                Health = CertificateExpiry.Evaluate(n.CertNotAfter, now, _expiry).ToString(),
                SupportsNodeCommands = n.SupportsNodeCommands,
                ManagementCapabilities = n.ManagementCapabilities,
                CurrentSessions = n.CurrentSessions
            }).ToList(),
            RecentCommands = recent.Select(c => new RecentNodeCommandDto
            {
                Id = c.Id,
                NodeId = c.NodeId,
                Type = c.Type.ToString(),
                Status = c.Status.ToString(),
                CreatedAt = c.CreatedAt,
                CreatedBy = c.CreatedBy,
                CompletedAt = c.CompletedAt,
                ResultCode = c.ResultCode
            }).ToList()
        };
    }
}
