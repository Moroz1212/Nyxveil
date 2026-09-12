using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class FleetOverviewService : IFleetOverviewService
{
    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly IClock _clock;
    private readonly IServerReleaseService _releases;
    private readonly CertificateExpiryOptions _certOpts;
    private readonly NodeHeartbeatOptions _hbOpts;

    public FleetOverviewService(
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        IClock clock,
        IServerReleaseService releases,
        IOptions<CertificateExpiryOptions>? certOpts = null,
        IOptions<NodeHeartbeatOptions>? hbOpts = null)
    {
        _dbFactory = dbFactory;
        _clock = clock;
        _releases = releases;
        _certOpts = certOpts?.Value ?? new CertificateExpiryOptions();
        _hbOpts = hbOpts?.Value ?? new NodeHeartbeatOptions();
    }

    public async Task<FleetOverviewDto> GetOverviewAsync(FleetQuery? query = null, CancellationToken cancellationToken = default)
    {
        query ??= new FleetQuery();
        var now = _clock.UtcNow;
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);

        var locations = await db.Locations.AsNoTracking()
            .OrderBy(l => l.SortOrder).ThenBy(l => l.Code)
            .ToListAsync(cancellationToken).ConfigureAwait(false);

        var nodes = await db.Nodes.AsNoTracking()
            .Where(n => n.LifecycleState != NodeLifecycleState.Deleted)
            .ToListAsync(cancellationToken).ConfigureAwait(false);

        var nodeIds = nodes.Select(n => n.NodeId).ToList();
        var health = await db.NodeHealth.AsNoTracking()
            .Where(h => nodeIds.Contains(h.NodeId))
            .ToDictionaryAsync(h => h.NodeId, cancellationToken).ConfigureAwait(false);
        var configs = await db.NodeConfigs.AsNoTracking()
            .Where(c => nodeIds.Contains(c.NodeId))
            .ToDictionaryAsync(c => c.NodeId, cancellationToken).ConfigureAwait(false);

        var activeCmd = await db.NodeCommands.AsNoTracking()
            .Where(c => nodeIds.Contains(c.NodeId) &&
                        (c.Status == NodeCommandStatus.Pending || c.Status == NodeCommandStatus.Claimed
                         || c.Status == NodeCommandStatus.Running || c.Status == NodeCommandStatus.Executing
                         || c.Status == NodeCommandStatus.Accepted))
            .GroupBy(c => c.NodeId)
            .Select(g => new { NodeId = g.Key, Type = g.OrderByDescending(x => x.CreatedAt).Select(x => x.Type).FirstOrDefault() })
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        var cmdByNode = activeCmd.ToDictionary(x => x.NodeId, x => x.Type.ToString());

        string? latest = null;
        try
        {
            var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
            if (release.SourceStatus is "ok" or "cached")
                latest = release.LatestVersion;
        }
        catch
        {
            // Fleet still renders without release metadata.
        }

        var productionNodes = nodes.Where(n => n.LifecycleState == NodeLifecycleState.Active).ToList();
        var rows = productionNodes.Select(n =>
        {
            health.TryGetValue(n.NodeId, out var h);
            configs.TryGetValue(n.NodeId, out var cfg);
            cmdByNode.TryGetValue(n.NodeId, out var op);
            return BuildNodeRow(n, h, cfg, op, latest, now);
        }).ToList();

        var byLoc = rows.GroupBy(r => r.LocationId, StringComparer.OrdinalIgnoreCase)
            .ToDictionary(g => g.Key, g => g.ToList(), StringComparer.OrdinalIgnoreCase);

        var cards = new List<FleetLocationCardDto>();
        foreach (var loc in locations)
        {
            byLoc.TryGetValue(loc.LocationId, out var locNodes);
            locNodes ??= new List<FleetNodeRowDto>();
            // Include only production (non-TestOnly) for location health severity; still show TestOnly in list.
            var prod = locNodes.Where(n => !n.TestOnly).ToList();
            var effective = prod.Count > 0 ? prod : locNodes;
            cards.Add(BuildLocationCard(loc, locNodes, effective));
        }

        // Locations with orphan nodes not in Locations table
        foreach (var orphan in byLoc.Keys.Except(locations.Select(l => l.LocationId), StringComparer.OrdinalIgnoreCase))
        {
            var locNodes = byLoc[orphan];
            var prod = locNodes.Where(n => !n.TestOnly).ToList();
            cards.Add(BuildLocationCard(new Location
            {
                LocationId = orphan,
                Code = orphan,
                DisplayName = orphan,
                Country = "",
                City = ""
            }, locNodes, prod.Count > 0 ? prod : locNodes));
        }

        cards = ApplyFilters(cards, query);
        cards = ApplySort(cards, query.Sort);

        var summary = BuildSummary(cards, rows);
        var versions = rows
            .GroupBy(r => string.IsNullOrWhiteSpace(r.ServerVersion) ? "unknown" : r.ServerVersion!.Trim(),
                StringComparer.OrdinalIgnoreCase)
            .Select(g => new FleetVersionBucketDto
            {
                Version = g.Key,
                Count = g.Count(),
                UpdateAvailable = g.Any(x => x.UpdateAvailable)
            })
            .OrderByDescending(v => v.Count)
            .ThenBy(v => v.Version, StringComparer.OrdinalIgnoreCase)
            .ToList();

        var attention = BuildAttention(cards, rows);

        return new FleetOverviewDto
        {
            GeneratedAt = now,
            Summary = summary,
            Locations = cards,
            VersionDistribution = versions,
            Attention = attention
        };
    }

    private FleetNodeRowDto BuildNodeRow(
        Node n, NodeHealth? h, NodeConfig? cfg, string? activeOp, string? latest, DateTime now)
    {
        var mode = RuntimeHealthPresentation.EvaluateMode(n, h, cfg, now, recentSuccessfulUpdate: false);
        var seen = n.LastSeenAt ?? h?.UpdatedAt;
        var freshness = NodeFreshness.Evaluate(seen, now, _hbOpts);
        var cert = CertificateExpiry.Evaluate(n.CertNotAfter, now, _certOpts);
        var days = CertificateExpiry.DaysRemaining(n.CertNotAfter, now);
        var updateAvail = !string.IsNullOrWhiteSpace(latest)
                          && SemVersion.TryParse(n.ServerVersion, out var cur)
                          && SemVersion.TryParse(latest, out var lat)
                          && cur.CompareTo(lat) < 0;

        return new FleetNodeRowDto
        {
            NodeId = n.NodeId,
            DisplayName = string.IsNullOrWhiteSpace(n.DisplayName) ? n.NodeId : n.DisplayName,
            LocationId = n.LocationId,
            Mode = mode,
            ServerVersion = n.ServerVersion,
            CurrentSessions = n.CurrentSessions,
            Capacity = n.Capacity,
            Freshness = freshness,
            FreshnessLabel = NodeFreshness.LabelRu(freshness, NodeFreshness.Age(seen, now)),
            TunOk = h?.TunReady,
            TlsOk = h?.TlsOk,
            QuicOk = h?.QuicOk,
            BridgeOk = h?.BridgeOk,
            TicketKeysOk = h?.TicketKeysLoaded,
            CpConnected = h?.CpConnected,
            Draining = n.Draining || (cfg?.Draining ?? false),
            Maintenance = cfg?.MaintenanceMode ?? false,
            TestOnly = n.TestOnly,
            UpdateAvailable = updateAvail,
            ActiveOperation = activeOp,
            CertificateHealth = cert.ToString(),
            CertDaysRemaining = days
        };
    }

    private static FleetLocationCardDto BuildLocationCard(
        Location loc, List<FleetNodeRowDto> allNodes, List<FleetNodeRowDto> healthBasis)
    {
        var healthy = healthBasis.Count(n => n.Mode is OperatorNodeMode.Healthy or OperatorNodeMode.Recovering);
        var offline = healthBasis.Count(n => n.Mode is OperatorNodeMode.Offline or OperatorNodeMode.Unknown);
        var degraded = healthBasis.Count(n => n.Mode is OperatorNodeMode.Degraded);
        var maint = healthBasis.All(n => n.Maintenance) && healthBasis.Count > 0;
        var sessions = allNodes.Sum(n => n.CurrentSessions);
        var capacityKnown = allNodes.Any(n => n.Capacity > 0);
        int? totalCap = capacityKnown ? allNodes.Sum(n => Math.Max(0, n.Capacity)) : null;
        int? remaining = totalCap is int tc ? Math.Max(0, tc - sessions) : null;
        double? usedPct = totalCap is > 0 ? Math.Round(100.0 * sessions / totalCap.Value, 1) : null;

        var health = DeriveLocationHealth(healthBasis, maint, healthy, offline, remaining, totalCap);
        var dominant = allNodes
            .Where(n => !string.IsNullOrWhiteSpace(n.ServerVersion))
            .GroupBy(n => n.ServerVersion!, StringComparer.OrdinalIgnoreCase)
            .OrderByDescending(g => g.Count())
            .Select(g => g.Key)
            .FirstOrDefault();

        var certAgg = AggregateCert(allNodes);
        var nearest = allNodes.Where(n => n.CertDaysRemaining.HasValue).Select(n => n.CertDaysRemaining!.Value)
            .DefaultIfEmpty().Min();
        int? nearestDays = allNodes.Any(n => n.CertDaysRemaining.HasValue) ? nearest : null;

        return new FleetLocationCardDto
        {
            LocationId = loc.LocationId,
            Code = loc.Code,
            DisplayName = string.IsNullOrWhiteSpace(loc.DisplayName) ? loc.Code : loc.DisplayName,
            Country = loc.Country ?? "",
            City = loc.City ?? "",
            Health = health,
            NodeCount = allNodes.Count,
            HealthyCount = healthy,
            DegradedCount = degraded,
            OfflineCount = offline,
            ActiveSessions = sessions,
            TotalCapacity = totalCap,
            RemainingCapacity = remaining,
            CapacityUsedPercent = usedPct,
            DominantVersion = dominant,
            UpdateAvailableCount = allNodes.Count(n => n.UpdateAvailable),
            CertificateAggregate = certAgg,
            NearestCertDaysRemaining = nearestDays,
            HasActiveOperation = allNodes.Any(n => !string.IsNullOrEmpty(n.ActiveOperation)),
            Nodes = allNodes.OrderBy(n => n.DisplayName, StringComparer.OrdinalIgnoreCase).ToList()
        };
    }

    public static FleetLocationHealth DeriveLocationHealth(
        IReadOnlyList<FleetNodeRowDto> basis,
        bool allMaintenance,
        int healthy,
        int offline,
        int? remaining,
        int? totalCap)
    {
        if (basis.Count == 0) return FleetLocationHealth.Offline;
        if (allMaintenance) return FleetLocationHealth.Maintenance;
        if (healthy == 0) return FleetLocationHealth.Offline;
        if (offline == 0 && basis.All(n => n.Mode is OperatorNodeMode.Healthy or OperatorNodeMode.Recovering or OperatorNodeMode.Draining))
            return FleetLocationHealth.Healthy;
        // Critical when remaining capacity is zero while sessions exist, or only one healthy left with offline siblings.
        if ((remaining is 0 && totalCap is > 0) || (healthy == 1 && offline > 0 && basis.Count > 1))
            return FleetLocationHealth.Critical;
        if (offline > 0 || basis.Any(n => n.Mode is OperatorNodeMode.Degraded))
            return FleetLocationHealth.Degraded;
        return FleetLocationHealth.Healthy;
    }

    private static string AggregateCert(IEnumerable<FleetNodeRowDto> nodes)
    {
        var statuses = nodes.Select(n => n.CertificateHealth).ToList();
        if (statuses.Count == 0) return nameof(CertificateHealthStatus.Unknown);
        if (statuses.Any(s => s == nameof(CertificateHealthStatus.Expired))) return nameof(CertificateHealthStatus.Expired);
        if (statuses.Any(s => s == nameof(CertificateHealthStatus.Critical))) return nameof(CertificateHealthStatus.Critical);
        if (statuses.Any(s => s == nameof(CertificateHealthStatus.ExpiringSoon))) return nameof(CertificateHealthStatus.ExpiringSoon);
        if (statuses.All(s => s == nameof(CertificateHealthStatus.Healthy))) return nameof(CertificateHealthStatus.Healthy);
        return nameof(CertificateHealthStatus.Unknown);
    }

    private static FleetSummaryDto BuildSummary(List<FleetLocationCardDto> cards, List<FleetNodeRowDto> rows)
    {
        int? totalCap = rows.Any(r => r.Capacity > 0) ? rows.Sum(r => Math.Max(0, r.Capacity)) : null;
        var sessions = rows.Sum(r => r.CurrentSessions);
        return new FleetSummaryDto
        {
            Locations = cards.Count,
            Nodes = rows.Count,
            Healthy = rows.Count(r => r.Mode is OperatorNodeMode.Healthy or OperatorNodeMode.Recovering),
            Degraded = rows.Count(r => r.Mode is OperatorNodeMode.Degraded),
            Offline = rows.Count(r => r.Mode is OperatorNodeMode.Offline or OperatorNodeMode.Unknown),
            Draining = rows.Count(r => r.Draining),
            Maintenance = rows.Count(r => r.Maintenance),
            ActiveSessions = sessions,
            TotalCapacity = totalCap,
            CapacityUsedPercent = totalCap is > 0 ? Math.Round(100.0 * sessions / totalCap.Value, 1) : null,
            UpdateAvailable = rows.Count(r => r.UpdateAvailable),
            CertificateWarnings = rows.Count(r =>
                r.CertificateHealth is nameof(CertificateHealthStatus.ExpiringSoon)
                    or nameof(CertificateHealthStatus.Critical)
                    or nameof(CertificateHealthStatus.Expired))
        };
    }

    private static List<FleetAttentionItemDto> BuildAttention(List<FleetLocationCardDto> cards, List<FleetNodeRowDto> rows)
    {
        var items = new List<FleetAttentionItemDto>();
        foreach (var c in cards.Where(c => c.Health is FleetLocationHealth.Critical or FleetLocationHealth.Offline))
        {
            items.Add(new FleetAttentionItemDto
            {
                Severity = "error",
                Kind = "location_" + c.Health.ToString().ToLowerInvariant(),
                Title = $"{c.DisplayName}: {c.Health}",
                LocationId = c.LocationId,
                Href = "/admin/fleet"
            });
        }

        foreach (var n in rows.Where(n => n.Mode is OperatorNodeMode.Offline || n.Freshness == DataFreshness.Stale).Take(40))
        {
            items.Add(new FleetAttentionItemDto
            {
                Severity = "warn",
                Kind = n.Mode == OperatorNodeMode.Offline ? "node_offline" : "stale_heartbeat",
                Title = $"{n.DisplayName}: {n.FreshnessLabel}",
                NodeId = n.NodeId,
                LocationId = n.LocationId,
                Href = $"/admin/nodes/{n.NodeId}"
            });
        }

        return items.Take(80).ToList();
    }

    private static List<FleetLocationCardDto> ApplyFilters(List<FleetLocationCardDto> cards, FleetQuery query)
    {
        var filter = (query.Filter ?? "all").Trim().ToLowerInvariant();
        IEnumerable<FleetLocationCardDto> q = cards;
        q = filter switch
        {
            "healthy" => q.Where(c => c.Health == FleetLocationHealth.Healthy),
            "degraded" => q.Where(c => c.Health == FleetLocationHealth.Degraded),
            "critical" => q.Where(c => c.Health == FleetLocationHealth.Critical),
            "offline" => q.Where(c => c.Health == FleetLocationHealth.Offline),
            "update" or "update available" => q.Where(c => c.UpdateAvailableCount > 0),
            "draining" => q.Where(c => c.Nodes.Any(n => n.Draining)),
            "maintenance" => q.Where(c => c.Health == FleetLocationHealth.Maintenance || c.Nodes.Any(n => n.Maintenance)),
            "certificate" or "certificate warning" => q.Where(c =>
                c.CertificateAggregate is nameof(CertificateHealthStatus.ExpiringSoon)
                    or nameof(CertificateHealthStatus.Critical)
                    or nameof(CertificateHealthStatus.Expired)),
            _ => q
        };

        if (!string.IsNullOrWhiteSpace(query.Search))
        {
            var s = query.Search.Trim();
            q = q.Where(c =>
                c.DisplayName.Contains(s, StringComparison.OrdinalIgnoreCase)
                || c.LocationId.Contains(s, StringComparison.OrdinalIgnoreCase)
                || c.Country.Contains(s, StringComparison.OrdinalIgnoreCase)
                || c.City.Contains(s, StringComparison.OrdinalIgnoreCase)
                || c.Nodes.Any(n =>
                    n.NodeId.Contains(s, StringComparison.OrdinalIgnoreCase)
                    || n.DisplayName.Contains(s, StringComparison.OrdinalIgnoreCase)
                    || (n.ServerVersion?.Contains(s, StringComparison.OrdinalIgnoreCase) ?? false)));
        }

        return q.ToList();
    }

    private static List<FleetLocationCardDto> ApplySort(List<FleetLocationCardDto> cards, string? sort) =>
        (sort ?? "status").Trim().ToLowerInvariant() switch
        {
            "name" => cards.OrderBy(c => c.DisplayName, StringComparer.OrdinalIgnoreCase).ToList(),
            "sessions" => cards.OrderByDescending(c => c.ActiveSessions).ThenBy(c => c.DisplayName).ToList(),
            "capacity" => cards.OrderByDescending(c => c.CapacityUsedPercent ?? -1).ThenBy(c => c.DisplayName).ToList(),
            "nodes" => cards.OrderByDescending(c => c.NodeCount).ThenBy(c => c.DisplayName).ToList(),
            _ => cards.OrderBy(c => c.Health).ThenBy(c => c.DisplayName, StringComparer.OrdinalIgnoreCase).ToList()
        };
}
