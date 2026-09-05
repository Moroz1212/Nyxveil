using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Tickets;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class TicketService : ITicketService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly AccessTicketService _issuer;
    private readonly IClock _clock;

    public TicketService(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        AccessTicketService issuer,
        IClock clock)
    {
        _db = db;
        _hasher = hasher;
        _issuer = issuer;
        _clock = clock;
    }

    public async Task<TicketIssueResponse> IssueAsync(
        TicketIssueRequest request,
        CancellationToken cancellationToken = default)
    {
        var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, request.LicenseToken, cancellationToken)
            .ConfigureAwait(false);
        var device = await LoadActiveDeviceAsync(lic.LicenseId, request.DeviceId, cancellationToken)
            .ConfigureAwait(false);

        var permissions = TicketScopeCalculator.PermissionsFromPlanJson(lic.Plan.Permissions);
        if (!TicketScopeCalculator.HasConnectPermission(permissions))
            throw new ForbiddenException("plan missing connect permission");

        var locations = lic.AllowedLocations.Select(a => a.LocationId).ToList();
        if (!string.IsNullOrWhiteSpace(request.LocationId))
        {
            // Accept LocationId or admin Code alias, emit canonical LocationId.
            var allLocs = await _db.Locations.AsNoTracking().ToListAsync(cancellationToken).ConfigureAwait(false);
            var canonical = LocationIdResolver.ResolveCanonicalId(allLocs, request.LocationId)
                            ?? throw new NotFoundException("location not found");

            if (locations.Count > 0 && !locations.Contains(canonical, StringComparer.Ordinal))
                throw new ForbiddenException("location not allowed");
            locations = new List<string> { canonical };
        }

        var nodeScope = new List<string>();
        if (!string.IsNullOrWhiteSpace(request.NodeId))
        {
            var node = await _db.Nodes.AsNoTracking()
                .FirstOrDefaultAsync(n => n.NodeId == request.NodeId, cancellationToken)
                .ConfigureAwait(false)
                ?? throw new NotFoundException("node not found");

            var nodeCfg = await _db.NodeConfigs.AsNoTracking()
                .FirstOrDefaultAsync(c => c.NodeId == node.NodeId, cancellationToken)
                .ConfigureAwait(false);
            if (nodeCfg is not null && !nodeCfg.Enabled)
                throw new NotFoundException("node not found");
            if (nodeCfg is null && !node.Enabled)
                throw new NotFoundException("node not found");

            if (locations.Count > 0 && !locations.Contains(node.LocationId, StringComparer.Ordinal))
                throw new ForbiddenException("node outside license locations");

            nodeScope.Add(node.NodeId);
        }

        var role = TicketScopeCalculator.NormalizeRole(lic.Role);

        var command = new IssueTicketCommand
        {
            LicenseId = LicenseIdFormat.ToPublicId(lic.LicenseId),
            DeviceId = device.ClientDeviceId,
            Role = role,
            Plan = lic.Plan.Code,
            Permissions = permissions,
            Locations = locations,
            NodeScope = nodeScope,
            DevicePublicKey = device.PublicKey
        };

        var (jwt, exp, jti) = await _issuer.IssueDetailedAsync(command, cancellationToken).ConfigureAwait(false);
        await WriteAuditAsync(jti, exp, lic.LicenseId, device.ClientDeviceId, locations, nodeScope, "issue", cancellationToken)
            .ConfigureAwait(false);

        device.LastSeenAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        return new TicketIssueResponse
        {
            AccessTicket = jwt,
            ExpiresAt = new DateTimeOffset(exp).ToUnixTimeSeconds(),
            NodeId = request.NodeId
        };
    }

    public async Task<TicketIssueResponse> RefreshAsync(
        TicketRefreshRequest request,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(request.AccessTicket))
            throw new ValidationException("access_ticket is required");

        var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, request.LicenseToken, cancellationToken)
            .ConfigureAwait(false);
        var device = await LoadActiveDeviceAsync(lic.LicenseId, request.DeviceId, cancellationToken)
            .ConfigureAwait(false);

        AccessTicketClaims old;
        try
        {
            old = _issuer.VerifyAccessTicket(request.AccessTicket);
        }
        catch (UnauthorizedAccessException ex)
        {
            throw new UnauthorizedException(ex.Message);
        }

        if (!string.Equals(old.DeviceId, request.DeviceId, StringComparison.Ordinal))
            throw new ForbiddenException("device mismatch");
        if (!string.Equals(old.LicenseId, LicenseIdFormat.ToPublicId(lic.LicenseId), StringComparison.Ordinal))
            throw new ForbiddenException("license mismatch");

        var revoked = await _db.Revocations.AsNoTracking().AnyAsync(
                r => (r.Type == RevocationType.Ticket && r.TargetId == old.Jti) ||
                     (r.Type == RevocationType.License && r.TargetId == old.LicenseId) ||
                     (r.Type == RevocationType.Device && r.TargetId == old.DeviceId),
                cancellationToken)
            .ConfigureAwait(false);
        if (revoked)
            throw new ForbiddenException("revoked");

        var currentLocations = lic.AllowedLocations.Select(a => a.LocationId).ToList();
        if (!TicketScopeCalculator.TryRefreshLocations(old.Locations, currentLocations, out var newLocations, out var locErr))
            throw new ForbiddenException(locErr!);

        IReadOnlyList<string> allowedNodes = Array.Empty<string>();
        if (!TicketScopeCalculator.TryRefreshNodeScope(old.NodeScope, allowedNodes, out var newNodeScope, out var nodeErr))
            throw new ForbiddenException(nodeErr!);

        var role = TicketScopeCalculator.NormalizeRole(lic.Role);
        if (role != old.Role && old.Role != "master")
            throw new ForbiddenException("role changed; open a new session");
        var permissions = TicketScopeCalculator.Intersect(old.Permissions,
            TicketScopeCalculator.PermissionsFromPlanJson(lic.Plan.Permissions));
        if (!TicketScopeCalculator.HasConnectPermission(permissions))
            throw new ForbiddenException("plan missing connect permission");

        var command = new IssueTicketCommand
        {
            LicenseId = LicenseIdFormat.ToPublicId(lic.LicenseId),
            DeviceId = device.ClientDeviceId,
            Role = role,
            Plan = lic.Plan.Code,
            Permissions = permissions,
            Locations = newLocations,
            NodeScope = newNodeScope,
            DevicePublicKey = device.PublicKey.Length == 32 ? device.PublicKey : (old.DevicePub ?? Array.Empty<byte>())
        };

        var (jwt, exp, jti) = await _issuer.IssueDetailedAsync(command, cancellationToken).ConfigureAwait(false);
        await WriteAuditAsync(
                jti, exp, lic.LicenseId, device.ClientDeviceId, newLocations, newNodeScope, "refresh", cancellationToken)
            .ConfigureAwait(false);

        device.LastSeenAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        return new TicketIssueResponse
        {
            AccessTicket = jwt,
            ExpiresAt = new DateTimeOffset(exp).ToUnixTimeSeconds()
        };
    }

    private async Task<Device> LoadActiveDeviceAsync(Guid licenseId, string deviceId, CancellationToken ct)
    {
        if (string.IsNullOrWhiteSpace(deviceId))
            throw new ValidationException("device_id is required");

        var device = await _db.Devices
            .FirstOrDefaultAsync(d => d.LicenseId == licenseId && d.ClientDeviceId == deviceId, ct)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("device not found");

        if (device.Status == DeviceStatus.Revoked)
            throw new ForbiddenException("device revoked");
        if (device.Status == DeviceStatus.Disabled)
            throw new ForbiddenException("device disabled");
        if (device.PublicKey.Length != 32)
            throw new ValidationException("device public key invalid");

        return device;
    }

    private async Task WriteAuditAsync(
        string ticketId,
        DateTime expiresAt,
        Guid licenseId,
        string deviceId,
        IReadOnlyList<string> locations,
        IReadOnlyList<string> nodeScope,
        string action,
        CancellationToken ct)
    {
        _db.TicketAudits.Add(new TicketAudit
        {
            Id = Guid.NewGuid(),
            TicketId = ticketId,
            LicenseId = licenseId,
            DeviceId = deviceId,
            IssuedAt = _clock.UtcNow,
            ExpiresAt = expiresAt,
            LocationsJson = JsonSerializer.Serialize(locations),
            NodeScopeJson = JsonSerializer.Serialize(nodeScope),
            Action = action
        });
        await _db.SaveChangesAsync(ct).ConfigureAwait(false);
    }
}
