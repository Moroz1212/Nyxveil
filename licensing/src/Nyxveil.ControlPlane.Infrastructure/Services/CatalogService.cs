using System.Security.Cryptography;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
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

    public async Task<SignedCatalogDto> GetSignedCatalogForCallerAsync(
        AccessTicketClaims? ticketClaims,
        string? licenseToken,
        CancellationToken cancellationToken = default)
    {
        string role;
        IReadOnlyList<string> allowedLocations;

        if (ticketClaims is not null)
        {
            role = ticketClaims.Role;
            allowedLocations = ticketClaims.Locations ?? Array.Empty<string>();
        }
        else if (!string.IsNullOrWhiteSpace(licenseToken))
        {
            var lic = await LicenseTokenHelper.LoadUsableAsync(_db, _hasher, licenseToken, cancellationToken)
                .ConfigureAwait(false);
            role = lic.Role;
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

        // Frozen Core: TestOnly visible/selectable ONLY when role == master.
        var canSeeTest = string.Equals(role, "master", StringComparison.OrdinalIgnoreCase);
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
                var profiles = MapProfiles(n.Transports);
                // MaintenanceMode → Draining=true so Frozen Core excludes without Core changes.
                var draining = (cfg?.Draining ?? n.Draining) || (cfg?.MaintenanceMode ?? false);
                var enabled = cfg?.Enabled ?? n.Enabled;
                return new NodeRegistryEntryDto
                {
                    NodeId = n.NodeId,
                    LocationId = n.LocationId,
                    Country = n.Location?.Country ?? string.Empty,
                    City = n.Location?.City ?? string.Empty,
                    DisplayName = n.DisplayName,
                    Status = n.Status.ToString().ToLowerInvariant(),
                    Enabled = enabled,
                    TestOnly = n.TestOnly,
                    Draining = draining,
                    ProtocolVersion = n.ProtocolVersion,
                    ServerVersion = n.ServerVersion ?? string.Empty,
                    ServerName = n.ServerName,
                    SpkiPin = n.SpkiPin,
                    Capacity = cfg?.Capacity > 0 ? Math.Min(n.Capacity, cfg.Capacity) : n.Capacity,
                    CurrentSessions = n.CurrentSessions,
                    LastSeen = n.LastSeenAt ?? default,
                    Endpoints = n.Endpoints.Where(e => e.Enabled).OrderBy(e => e.Priority)
                        .Select(e => new EndpointDto
                        {
                            Host = e.Host,
                            Port = e.Port,
                            IpFamily = MapIpFamily(e.AddressFamily),
                            Profiles = profiles
                        }).ToList(),
                    Health = new HealthInfoDto
                    {
                        Healthy = h?.Healthy ?? false,
                        SessionCount = h?.ActiveSessions ?? n.CurrentSessions,
                        CpuPercent = h?.CpuPercent ?? 0,
                        MemoryPercent = h?.MemoryPercent ?? 0
                    }
                };
            }).ToList()
        };

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
