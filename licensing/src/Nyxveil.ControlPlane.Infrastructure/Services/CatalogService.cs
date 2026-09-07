using System.Security.Cryptography;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Application.Tickets;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class CatalogService : ICatalogService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ICatalogSigner _signer;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;

    public CatalogService(
        ControlPlaneDbContext db,
        ICatalogSigner signer,
        ILicenseKeyHasher hasher,
        IClock clock)
    {
        _db = db;
        _signer = signer;
        _hasher = hasher;
        _clock = clock;
    }

    public async Task<SignedCatalogDto> GetSignedCatalogForNodeAsync(
        string nodeId,
        CancellationToken cancellationToken = default)
    {
        var node = await _db.Nodes.AsNoTracking()
            .Where(n => n.NodeId == nodeId && n.LifecycleState == NodeLifecycleState.Active)
            .Include(n => n.Endpoints)
            .Include(n => n.Transports)
            .Include(n => n.Location)
            .SingleOrDefaultAsync(cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("active node not found");

        var config = await _db.NodeConfigs.AsNoTracking()
            .SingleOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        if (!(config?.Enabled ?? node.Enabled) || node.Location is null || !node.Location.Enabled)
            throw new ForbiddenException("node or location disabled");

        var health = await _db.NodeHealth.AsNoTracking()
            .SingleOrDefaultAsync(h => h.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        var now = _clock.UtcNow;
        var catalog = new CatalogDto
        {
            Version = "cat_" + Convert.ToHexString(RandomNumberGenerator.GetBytes(8)).ToLowerInvariant(),
            IssuedAt = now,
            ExpiresAt = now.AddHours(1),
            Locations =
            [
                new LocationDto
                {
                    LocationId = node.Location.LocationId,
                    Country = node.Location.Country,
                    CountryCode = node.Location.CountryCode ?? string.Empty,
                    City = node.Location.City,
                    DisplayName = node.Location.DisplayName,
                    Enabled = node.Location.Enabled
                }
            ],
            Nodes = [ProjectNode(node, config, health)]
        };

        return await SignAndRecordAsync(catalog, now, cancellationToken).ConfigureAwait(false);
    }

    public async Task<SignedCatalogDto> GetSignedCatalogForCallerAsync(
        AccessTicketClaims? ticketClaims,
        string? licenseToken,
        CancellationToken cancellationToken = default)
    {
        string role;
        IReadOnlyList<string> allowedLocations;
        bool masterActive;

        if (ticketClaims is not null)
        {
            if (!LicenseIdFormat.TryParse(ticketClaims.LicenseId, out var licenseId))
                throw new UnauthorizedException("invalid license identity");
            var lic = await _db.Licenses.AsNoTracking().Include(l => l.AllowedLocations)
                .SingleOrDefaultAsync(l => l.LicenseId == licenseId, cancellationToken)
                ?? throw new UnauthorizedException("unknown license");
            LicenseTokenHelper.EnsureUsable(lic);
            if (await _db.Revocations.AnyAsync(r =>
                    (r.Type == RevocationType.Ticket && r.TargetId == ticketClaims.Jti) ||
                    (r.Type == RevocationType.License && r.TargetId == ticketClaims.LicenseId) ||
                    (r.Type == RevocationType.Device && r.TargetId == ticketClaims.DeviceId), cancellationToken) ||
                !await _db.Devices.AnyAsync(d => d.LicenseId == licenseId && d.ClientDeviceId == ticketClaims.DeviceId &&
                    d.Status == DeviceStatus.Active, cancellationToken))
                throw new ForbiddenException("revoked or disabled caller");
            role = ticketClaims.Role;
            masterActive = lic.Status == LicenseStatus.Active && lic.Role == "master" && role == "master";
            if (!TicketScopeCalculator.TryRefreshLocations(ticketClaims.Locations,
                    lic.AllowedLocations.Select(a => a.LocationId).ToList(), out allowedLocations, out var error))
                throw new ForbiddenException(error!);
        }
        else if (!string.IsNullOrWhiteSpace(licenseToken))
        {
            var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, licenseToken, cancellationToken)
                .ConfigureAwait(false);
            role = lic.Role;
            masterActive = lic.Status == LicenseStatus.Active && role == "master";
            allowedLocations = lic.AllowedLocations.Select(a => a.LocationId).ToList();
        }
        else
        {
            throw new UnauthorizedException("catalog requires ticket or license token");
        }

        var locations = await _db.Locations.AsNoTracking()
            .Where(l => l.Enabled)
            .OrderBy(l => l.SortOrder)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var nodes = await _db.Nodes.AsNoTracking()
            .Where(n => n.LifecycleState == NodeLifecycleState.Active)
            .Include(n => n.Endpoints)
            .Include(n => n.Transports)
            .Include(n => n.Location)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        var configs = await _db.NodeConfigs.AsNoTracking()
            .ToDictionaryAsync(c => c.NodeId, cancellationToken)
            .ConfigureAwait(false);

        var health = await _db.NodeHealth.AsNoTracking()
            .ToDictionaryAsync(h => h.NodeId, cancellationToken)
            .ConfigureAwait(false);

        var allow = allowedLocations.Where(x => !string.IsNullOrWhiteSpace(x)).Distinct(StringComparer.Ordinal).ToList();

        if (allow.Count > 0)
        {
            // Security filter uses LocationId only (Code is admin alias, not catalog scope).
            locations = locations
                .Where(l => allow.Contains(l.LocationId, StringComparer.Ordinal))
                .ToList();
            var locIds = locations.Select(l => l.LocationId).ToHashSet(StringComparer.Ordinal);
            nodes = nodes.Where(n => locIds.Contains(n.LocationId)).ToList();
        }

        var enabledLocationIds = locations.Select(l => l.LocationId).ToHashSet(StringComparer.Ordinal);
        nodes = nodes.Where(n => enabledLocationIds.Contains(n.LocationId)).ToList();

        // Frozen Core: TestOnly visible/selectable ONLY when role == master.
        var canSeeTest = masterActive;
        if (!canSeeTest)
            nodes = nodes.Where(n => !n.TestOnly).ToList();

        // Authoritative NodeConfig: exclude disabled; maintenance projects as draining for Frozen selector.
        nodes = nodes.Where(n =>
        {
            if (!configs.TryGetValue(n.NodeId, out var cfg))
                return n.Enabled;
            return cfg.Enabled;
        }).ToList();

        // Only publish nodes with at least one enabled production transport.
        nodes = nodes.Where(n => n.Transports.Any(t => t.Enabled)).ToList();

        var now = _clock.UtcNow;
        var version = "cat_" + Convert.ToHexString(RandomNumberGenerator.GetBytes(8)).ToLowerInvariant();

        var catalog = new CatalogDto
        {
            Version = version,
            IssuedAt = now,
            ExpiresAt = now.AddHours(1),
            Locations = locations.Select(l => new LocationDto
            {
                LocationId = l.LocationId,
                Country = l.Country,
                CountryCode = l.CountryCode ?? string.Empty,
                City = l.City,
                DisplayName = l.DisplayName,
                Enabled = l.Enabled
            }).ToList(),
            Nodes = nodes.Select(n =>
            {
                health.TryGetValue(n.NodeId, out var h);
                configs.TryGetValue(n.NodeId, out var cfg);
                return ProjectNode(n, cfg, h);
            }).ToList()
        };

        return await SignAndRecordAsync(catalog, now, cancellationToken).ConfigureAwait(false);
    }

    private static NodeRegistryEntryDto ProjectNode(Node node, NodeConfig? cfg, NodeHealth? health)
    {
        var profiles = MapProfiles(node.Transports);
        // MaintenanceMode → Draining=true so Frozen Core excludes without Core changes.
        var draining = (cfg?.Draining ?? node.Draining) || (cfg?.MaintenanceMode ?? false);
        return new NodeRegistryEntryDto
        {
            NodeId = node.NodeId,
            LocationId = node.LocationId,
            Country = node.Location?.Country ?? string.Empty,
            City = node.Location?.City ?? string.Empty,
            DisplayName = node.DisplayName,
            Status = node.Status.ToString().ToLowerInvariant(),
            Enabled = cfg?.Enabled ?? node.Enabled,
            TestOnly = node.TestOnly,
            Draining = draining,
            ProtocolVersion = node.ProtocolVersion,
            ServerVersion = node.ServerVersion ?? string.Empty,
            ServerName = string.IsNullOrEmpty(node.ServerName) ? null : node.ServerName,
            SpkiPin = node.SpkiPin is { Length: > 0 } ? node.SpkiPin : null,
            Capacity = cfg is not null ? Math.Min(node.Capacity, cfg.Capacity) : node.Capacity,
            CurrentSessions = node.CurrentSessions,
            LastSeen = NormalizeUtcTimestamp(node.LastSeenAt),
            Endpoints = node.Endpoints.Where(e => e.Enabled)
                .GroupBy(e => $"{e.Host}|{e.Port}|{MapIpFamily(e.AddressFamily)}", StringComparer.OrdinalIgnoreCase)
                .Select(g => g.OrderBy(e => e.Priority).First())
                .OrderBy(e => e.Priority)
                .Select(e => new EndpointDto
                {
                    Host = e.Host,
                    Port = e.Port,
                    IpFamily = MapIpFamily(e.AddressFamily),
                    Profiles = profiles
                }).ToList(),
            Health = new HealthInfoDto
            {
                Healthy = health?.Healthy ?? false,
                SessionCount = health?.ActiveSessions ?? node.CurrentSessions,
                CpuPercent = health?.CpuPercent ?? 0,
                MemoryPercent = health?.MemoryPercent ?? 0
            }
        };
    }

    private async Task<SignedCatalogDto> SignAndRecordAsync(
        CatalogDto catalog,
        DateTime now,
        CancellationToken cancellationToken)
    {
        var signed = await _signer.SignAsync(catalog, cancellationToken).ConfigureAwait(false);
        var payload = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
        var hash = Convert.ToHexString(SHA256.HashData(payload)).ToLowerInvariant();

        _db.CatalogVersions.Add(new CatalogVersion
        {
            Id = Guid.NewGuid(),
            Version = catalog.Version,
            IssuedAt = catalog.IssuedAt,
            ExpiresAt = catalog.ExpiresAt,
            KeyId = signed.KeyId,
            PayloadHash = hash,
            CreatedAt = now
        });
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        return signed;
    }

    /// <summary>
    /// EF / SQL often return Unspecified UTC wall times. Treat them as UTC (never host-local).
    /// </summary>
    internal static DateTime NormalizeUtcTimestamp(DateTime? value)
    {
        var dt = value ?? default;
        return dt.Kind switch
        {
            DateTimeKind.Utc => dt,
            DateTimeKind.Local => dt.ToUniversalTime(),
            _ => DateTime.SpecifyKind(dt, DateTimeKind.Utc)
        };
    }

    internal static IReadOnlyList<string> MapProfiles(IEnumerable<NodeTransport> transports)
    {
        var profiles = new List<string>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var t in transports.Where(x => x.Enabled).OrderBy(x => x.Priority))
        {
            var profile = t.TransportType.Trim().ToLowerInvariant() switch
            {
                "quic" => "quic-udp-443",
                "tls" => "tls-tcp-443",
                _ => null
            };
            if (profile is null)
                continue;
            if (seen.Add(profile))
                profiles.Add(profile);
        }

        return profiles;
    }

    /// <summary>Frozen Core expects ipv4 / ipv6 / dual — map hostname storage to dual.</summary>
    internal static string MapIpFamily(string? addressFamily)
    {
        if (string.IsNullOrWhiteSpace(addressFamily))
            return "dual";

        return addressFamily.Trim().ToLowerInvariant() switch
        {
            "ipv4" => "ipv4",
            "ipv6" => "ipv6",
            "dual" => "dual",
            "hostname" => "dual",
            _ => "dual"
        };
    }
}
