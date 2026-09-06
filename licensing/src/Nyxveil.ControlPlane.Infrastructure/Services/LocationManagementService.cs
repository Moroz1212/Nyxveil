using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class LocationManagementService : ILocationManagementService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IAuditService _audit;

    public LocationManagementService(ControlPlaneDbContext db, IAuditService audit)
    {
        _db = db;
        _audit = audit;
    }

    public async Task DeleteLocationAsync(string locationId, string actor, CancellationToken cancellationToken = default)
    {
        var location = await _db.Locations.FirstOrDefaultAsync(l => l.LocationId == locationId, cancellationToken)
            .ConfigureAwait(false) ?? throw new NotFoundException("location not found");
        if (await _db.Nodes.AnyAsync(
                n => n.LocationId == locationId && n.LifecycleState != NodeLifecycleState.Deleted,
                cancellationToken).ConfigureAwait(false))
            throw new ValidationException("location is referenced by non-deleted nodes");

        _db.Locations.Remove(location);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = string.IsNullOrWhiteSpace(actor) ? "admin" : actor,
            Action = "location.deleted",
            EntityType = "Location",
            EntityId = locationId
        }, cancellationToken).ConfigureAwait(false);
    }
}
