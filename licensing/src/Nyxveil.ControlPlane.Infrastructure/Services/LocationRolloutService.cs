using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>
/// Sequential location update using existing UpdateNodeLatest + location safety.
/// Progress persisted in SystemSettings (no schema migration).
/// </summary>
public sealed class LocationRolloutService : ILocationRolloutService
{
    public const string SettingKeyPrefix = "location_rollout:";
    private static readonly JsonSerializerOptions JsonOpts = new() { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };

    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly INodeCommandService _commands;
    private readonly IServerReleaseService _releases;
    private readonly IClock _clock;
    private readonly IAuditService _audit;
    private readonly ILogger<LocationRolloutService> _log;
    private readonly ICriticalOperationAuthorizer _criticalOps;

    public LocationRolloutService(
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        INodeCommandService commands,
        IServerReleaseService releases,
        IClock clock,
        IAuditService audit,
        ILogger<LocationRolloutService> log,
        ICriticalOperationAuthorizer? criticalOps = null)
    {
        _dbFactory = dbFactory;
        _commands = commands;
        _releases = releases;
        _clock = clock;
        _audit = audit;
        _log = log;
        _criticalOps = criticalOps ?? AllowAllCriticalOperationAuthorizer.Instance;
    }

    public async Task<LocationRolloutDto> StartAsync(
        string locationId,
        string actor,
        IEnumerable<string> roles,
        CancellationToken cancellationToken = default)
    {
        _criticalOps.AssertAllowed(CriticalOperation.RollingUpdateStart, roles);
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
        if (release.SourceStatus is not ("ok" or "cached") || string.IsNullOrWhiteSpace(release.LatestVersion))
            throw new InvalidOperationException("latest stable server release is unknown; refresh Server Releases and retry");

        var nodes = await db.Nodes.AsNoTracking()
            .Where(n => n.LocationId == locationId && n.LifecycleState == NodeLifecycleState.Active && n.Enabled)
            .OrderBy(n => n.DisplayName).ThenBy(n => n.NodeId)
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        if (nodes.Count == 0)
            throw new InvalidOperationException("location has no eligible nodes");

        var existing = await LoadStateAsync(db, locationId, cancellationToken).ConfigureAwait(false);
        if (existing is { Status: LocationRolloutStatus.Running or LocationRolloutStatus.Pending })
            throw new InvalidOperationException("another disruptive command is already active in this location; wait for it to finish");

        var state = new RolloutState
        {
            Id = Guid.NewGuid(),
            LocationId = locationId,
            TargetVersion = release.LatestVersion,
            Status = LocationRolloutStatus.Running,
            CreatedBy = actor,
            CreatedAt = _clock.UtcNow,
            Items = nodes.Select(n => new RolloutItemState
            {
                NodeId = n.NodeId,
                DisplayName = string.IsNullOrWhiteSpace(n.DisplayName) ? n.NodeId : n.DisplayName,
                Status = LocationRolloutItemStatus.Pending
            }).ToList()
        };

        await SaveStateAsync(db, state, cancellationToken).ConfigureAwait(false);
        await _audit.WriteAsync(new AuditWriteRequest
        {
            Actor = actor,
            Action = "location.rollout.start",
            EntityType = "Location",
            EntityId = locationId,
            Detail = JsonSerializer.Serialize(new
            {
                state.Id,
                state.TargetVersion,
                nodes = state.Items.Select(i => i.NodeId)
            }, JsonOpts)
        }, cancellationToken).ConfigureAwait(false);

        await AdvanceAsync(state, actor, roles, cancellationToken).ConfigureAwait(false);
        return ToDto(state);
    }

    public async Task<LocationRolloutDto?> GetAsync(Guid rolloutId, CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var all = await db.SystemSettings.AsNoTracking()
            .Where(s => s.Key.StartsWith(SettingKeyPrefix))
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        foreach (var row in all)
        {
            var state = Deserialize(row.Value);
            if (state?.Id == rolloutId)
                return ToDto(state);
        }
        return null;
    }

    public async Task<IReadOnlyList<LocationRolloutDto>> ListRecentAsync(int take = 20, CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var rows = await db.SystemSettings.AsNoTracking()
            .Where(s => s.Key.StartsWith(SettingKeyPrefix))
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        return rows.Select(r => Deserialize(r.Value))
            .Where(s => s is not null)
            .Cast<RolloutState>()
            .OrderByDescending(s => s.CreatedAt)
            .Take(take)
            .Select(ToDto)
            .ToList();
    }

    public async Task TickAsync(CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var rows = await db.SystemSettings
            .Where(s => s.Key.StartsWith(SettingKeyPrefix))
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        foreach (var row in rows)
        {
            var state = Deserialize(row.Value);
            if (state is null || state.Status != LocationRolloutStatus.Running)
                continue;
            try
            {
                await AdvanceAsync(state, state.CreatedBy, new[] { AdminRole.SuperAdmin }, cancellationToken)
                    .ConfigureAwait(false);
            }
            catch (Exception ex)
            {
                _log.LogWarning(ex, "Location rollout tick failed for {Location}", state.LocationId);
            }
        }
    }

    private async Task AdvanceAsync(RolloutState state, string actor, IEnumerable<string> roles, CancellationToken ct)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(ct).ConfigureAwait(false);
        var running = state.Items.FirstOrDefault(i => i.Status == LocationRolloutItemStatus.Running);
        if (running is not null && running.CommandId is Guid cmdId)
        {
            var cmd = await db.NodeCommands.AsNoTracking().FirstOrDefaultAsync(c => c.Id == cmdId, ct)
                .ConfigureAwait(false);
            if (cmd is null)
            {
                Stop(state, running, "Команда обновления исчезла.");
            }
            else if (cmd.Status is NodeCommandStatus.Pending or NodeCommandStatus.Claimed
                     or NodeCommandStatus.Running or NodeCommandStatus.Executing or NodeCommandStatus.Accepted)
            {
                await SaveStateAsync(db, state, ct).ConfigureAwait(false);
                return;
            }
            else if (cmd.Status == NodeCommandStatus.Succeeded
                     && string.Equals(cmd.ResultCode, "updated_healthy", StringComparison.OrdinalIgnoreCase))
            {
                // Require fresh healthy sibling-style health on this node before next.
                var node = await db.Nodes.AsNoTracking().FirstAsync(n => n.NodeId == running.NodeId, ct)
                    .ConfigureAwait(false);
                var health = await db.NodeHealth.AsNoTracking().FirstOrDefaultAsync(h => h.NodeId == running.NodeId, ct)
                    .ConfigureAwait(false);
                var now = _clock.UtcNow;
                var fresh = NodeFreshness.Evaluate(node.LastSeenAt ?? health?.UpdatedAt, now) == DataFreshness.Fresh;
                var ok = node.Status == NodeRuntimeStatus.Healthy
                         && fresh
                         && health?.Healthy != false
                         && health?.TlsOk != false
                         && health?.TunReady != false
                         && health?.CpConnected != false;
                if (!ok)
                {
                    // Wait for recovering window up to UpdateRunningTtl-ish; if still bad after 10 min from complete, stop.
                    if (cmd.CompletedAt is not null && now - cmd.CompletedAt.Value > TimeSpan.FromMinutes(10))
                    {
                        Stop(state, running, "Node не восстановил runtime health после updated_healthy.");
                    }
                    else
                    {
                        running.Detail = "Ожидание восстановления runtime health…";
                        await SaveStateAsync(db, state, ct).ConfigureAwait(false);
                        return;
                    }
                }
                else
                {
                    running.Status = LocationRolloutItemStatus.Succeeded;
                    running.Detail = cmd.ResultCode;
                }
            }
            else
            {
                var reason = $"Остановка rolling update: {cmd.Status} / {cmd.ResultCode ?? "no_code"}";
                Stop(state, running, reason);
            }
        }

        if (state.Status is LocationRolloutStatus.Failed or LocationRolloutStatus.Stopped or LocationRolloutStatus.Succeeded)
        {
            await SaveStateAsync(db, state, ct).ConfigureAwait(false);
            return;
        }

        var next = state.Items.FirstOrDefault(i => i.Status == LocationRolloutItemStatus.Pending);
        if (next is null)
        {
            state.Status = state.Items.Any(i => i.Status == LocationRolloutItemStatus.Failed)
                ? LocationRolloutStatus.Failed
                : LocationRolloutStatus.Succeeded;
            state.CompletedAt = _clock.UtcNow;
            await SaveStateAsync(db, state, ct).ConfigureAwait(false);
            return;
        }

        // Re-check location safety implicitly via EnqueueAsync.
        // Start already required step-up; worker ticks must not re-prompt MFA per node.
        try
        {
            using (RolloutContinuationScope.Begin())
            {
                var cmd = await _commands.EnqueueAsync(next.NodeId, NodeCommandType.UpdateNodeLatest, actor, roles, ct)
                    .ConfigureAwait(false);
                next.Status = LocationRolloutItemStatus.Running;
                next.CommandId = cmd.Id;
                next.Detail = "UpdateNodeLatest enqueued";
                state.Status = LocationRolloutStatus.Running;
            }
        }
        catch (Exception ex)
        {
            Stop(state, next, UiSafe(ex.Message));
        }

        await SaveStateAsync(db, state, ct).ConfigureAwait(false);
    }

    private static void Stop(RolloutState state, RolloutItemState item, string reason)
    {
        item.Status = LocationRolloutItemStatus.Failed;
        item.Detail = reason;
        state.Status = LocationRolloutStatus.Stopped;
        state.StopReason = reason;
        state.CompletedAt = DateTime.UtcNow;
        foreach (var pending in state.Items.Where(i => i.Status == LocationRolloutItemStatus.Pending))
        {
            pending.Status = LocationRolloutItemStatus.Skipped;
            pending.Detail = "Пропущено из-за остановки rolling update";
        }
    }

    private static string UiSafe(string message) => message;

    private static async Task<RolloutState?> LoadStateAsync(ControlPlaneDbContext db, string locationId, CancellationToken ct)
    {
        var key = SettingKeyPrefix + locationId;
        var row = await db.SystemSettings.AsNoTracking().FirstOrDefaultAsync(s => s.Key == key, ct)
            .ConfigureAwait(false);
        return row is null ? null : Deserialize(row.Value);
    }

    private static async Task SaveStateAsync(ControlPlaneDbContext db, RolloutState state, CancellationToken ct)
    {
        var key = SettingKeyPrefix + state.LocationId;
        var json = JsonSerializer.Serialize(state, JsonOpts);
        var row = await db.SystemSettings.FirstOrDefaultAsync(s => s.Key == key, ct).ConfigureAwait(false);
        if (row is null)
        {
            db.SystemSettings.Add(new SystemSetting { Key = key, Value = json, UpdatedAt = DateTime.UtcNow });
        }
        else
        {
            row.Value = json;
            row.UpdatedAt = DateTime.UtcNow;
        }
        await db.SaveChangesAsync(ct).ConfigureAwait(false);
    }

    private static RolloutState? Deserialize(string? json)
    {
        if (string.IsNullOrWhiteSpace(json)) return null;
        try { return JsonSerializer.Deserialize<RolloutState>(json, JsonOpts); }
        catch { return null; }
    }

    private static LocationRolloutDto ToDto(RolloutState s) => new()
    {
        Id = s.Id,
        LocationId = s.LocationId,
        TargetVersion = s.TargetVersion,
        Status = s.Status.ToString(),
        CreatedBy = s.CreatedBy,
        CreatedAt = s.CreatedAt,
        CompletedAt = s.CompletedAt,
        StopReason = s.StopReason,
        Items = s.Items.Select(i => new LocationRolloutItemDto
        {
            NodeId = i.NodeId,
            DisplayName = i.DisplayName,
            Status = i.Status.ToString(),
            CommandId = i.CommandId,
            Detail = i.Detail
        }).ToList()
    };

    private sealed class RolloutState
    {
        public Guid Id { get; set; }
        public string LocationId { get; set; } = "";
        public string? TargetVersion { get; set; }
        public LocationRolloutStatus Status { get; set; }
        public string CreatedBy { get; set; } = "";
        public DateTime CreatedAt { get; set; }
        public DateTime? CompletedAt { get; set; }
        public string? StopReason { get; set; }
        public List<RolloutItemState> Items { get; set; } = new();
    }

    private sealed class RolloutItemState
    {
        public string NodeId { get; set; } = "";
        public string DisplayName { get; set; } = "";
        public LocationRolloutItemStatus Status { get; set; }
        public Guid? CommandId { get; set; }
        public string? Detail { get; set; }
    }
}
