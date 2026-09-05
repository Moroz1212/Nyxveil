using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class AuditService : IAuditService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;

    public AuditService(ControlPlaneDbContext db, IClock clock)
    {
        _db = db;
        _clock = clock;
    }

    public async Task WriteAsync(AuditWriteRequest request, CancellationToken cancellationToken = default)
    {
        _db.AuditLog.Add(new AuditLogEntry
        {
            Id = Guid.NewGuid(),
            Actor = string.IsNullOrWhiteSpace(request.Actor) ? "system" : request.Actor,
            Action = request.Action,
            EntityType = request.EntityType ?? string.Empty,
            EntityId = request.EntityId,
            Timestamp = _clock.UtcNow,
            IpAddress = request.IpAddress,
            DetailsJson = request.Detail
        });
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }
}
