using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class UpdatePreflightService : IUpdatePreflightService
{
    private readonly IDbContextFactory<ControlPlaneDbContext> _dbFactory;
    private readonly IClock _clock;
    private readonly IServerReleaseService _releases;
    private readonly NodeHeartbeatOptions _heartbeat;

    public UpdatePreflightService(
        IDbContextFactory<ControlPlaneDbContext> dbFactory,
        IClock clock,
        IServerReleaseService releases,
        IOptions<NodeHeartbeatOptions>? heartbeat = null)
    {
        _dbFactory = dbFactory;
        _clock = clock;
        _releases = releases;
        _heartbeat = heartbeat?.Value ?? new NodeHeartbeatOptions();
    }

    public async Task<UpdatePreflightResult> EvaluateUpdateAsync(string nodeId, CancellationToken cancellationToken = default)
    {
        await using var db = await _dbFactory.CreateDbContextAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var node = await db.Nodes.AsNoTracking().FirstOrDefaultAsync(n => n.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        if (node is null || node.LifecycleState == NodeLifecycleState.Deleted)
        {
            return new UpdatePreflightResult
            {
                NodeId = nodeId,
                CanEnqueue = false,
                BlockReason = "Сервер не найден.",
                LocationSafe = false,
                LocationSafetyDetail = "Сервер не найден.",
                DrainWaitSeconds = (int)NodeCommandService.UpdateDrainWait.TotalSeconds,
                DrainPolicyText = DrainPolicyText()
            };
        }

        var health = await db.NodeHealth.AsNoTracking().FirstOrDefaultAsync(h => h.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        var release = await _releases.GetLatestAsync(cancellationToken).ConfigureAwait(false);
        var installed = NodeVersionEvaluator.EffectiveInstalledVersion(node.ReportedServerVersion, node.ServerVersion);
        var target = release.LatestVersion;
        var tag = string.IsNullOrWhiteSpace(target) ? null : "server-v" + target;

        var siblings = await db.Nodes.AsNoTracking()
            .Where(n => n.LocationId == node.LocationId && n.NodeId != node.NodeId && n.LifecycleState == NodeLifecycleState.Active)
            .ToListAsync(cancellationToken).ConfigureAwait(false);
        var siblingConfigs = await db.NodeConfigs.AsNoTracking()
            .Where(c => siblings.Select(s => s.NodeId).Contains(c.NodeId))
            .ToDictionaryAsync(c => c.NodeId, cancellationToken).ConfigureAwait(false);

        var eligible = siblings.Where(s =>
            NodeCommandService.IsEligibleHealthySibling(s, siblingConfigs.GetValueOrDefault(s.NodeId), now)).ToList();
        var freeCap = eligible.Sum(s => Math.Max(0, s.Capacity - s.CurrentSessions));

        string? block = null;
        var locationSafe = true;
        try
        {
            // Reuse production safety by probing via shared helpers without enqueue.
            if (!NodeCommandCapabilities.Supports(node, NodeCommandType.UpdateNodeLatest))
            {
                locationSafe = false;
                block = "Сервер не подтвердил поддержку удалённого обновления.";
            }
            else if (eligible.Count == 0)
            {
                locationSafe = false;
                block = "Операция запрещена: в этой локации нет другого здорового сервера со свободной ёмкостью.";
            }
            else
            {
                var activeDisruptive = await db.NodeCommands.AsNoTracking()
                    .Where(c => siblings.Select(s => s.NodeId).Append(node.NodeId).Contains(c.NodeId))
                    .Where(c => c.Type == NodeCommandType.UpdateNodeLatest
                                || c.Type == NodeCommandType.RestartNyxveilService
                                || c.Type == NodeCommandType.RebootHost)
                    .Where(c => c.Status == NodeCommandStatus.Pending
                                || c.Status == NodeCommandStatus.Claimed
                                || c.Status == NodeCommandStatus.Running
                                || c.Status == NodeCommandStatus.Executing
                                || c.Status == NodeCommandStatus.Accepted
                                || (c.ResultCode != null && (
                                    c.ResultCode == "expired_outcome_unknown"
                                    || c.ResultCode == "outcome_unknown"
                                    || c.ResultCode == "rollback_failed")))
                    .AnyAsync(cancellationToken).ConfigureAwait(false);
                if (activeDisruptive)
                {
                    locationSafe = false;
                    block = "В этой локации уже выполняется операция. Дождитесь её завершения.";
                }
            }

            if (release.SourceStatus is not ("ok" or "cached") || string.IsNullOrWhiteSpace(target))
            {
                locationSafe = false;
                block ??= "Не удалось подтвердить стабильный выпуск. Обновите сведения о GitHub Releases.";
            }
        }
        catch (Exception ex)
        {
            locationSafe = false;
            block = ex.Message;
        }

        var hbAge = NodeFreshness.Age(node.LastSeenAt ?? health?.UpdatedAt, now);
        var hbFresh = NodeFreshness.Evaluate(node.LastSeenAt ?? health?.UpdatedAt, now, _heartbeat) == DataFreshness.Fresh;

        var can = locationSafe && block is null && node.LifecycleState == NodeLifecycleState.Active;
        return new UpdatePreflightResult
        {
            NodeId = node.NodeId,
            DisplayName = string.IsNullOrWhiteSpace(node.DisplayName) ? node.NodeId : node.DisplayName,
            LocationId = node.LocationId,
            InstalledVersion = installed,
            TargetVersion = target,
            ReleaseTag = tag,
            HeartbeatAgeText = NodeFreshness.FormatAgeRu(hbAge),
            HeartbeatFresh = hbFresh,
            Tun = RuntimeHealthPresentation.FlagLabelRu(RuntimeHealthPresentation.FromNullable(health?.TunReady)),
            TlsRuntime = RuntimeHealthPresentation.FlagLabelRu(RuntimeHealthPresentation.FromNullable(health?.TlsOk)),
            Quic = RuntimeHealthPresentation.FlagLabelRu(RuntimeHealthPresentation.FromNullable(health?.QuicOk)),
            Cp = RuntimeHealthPresentation.FlagLabelRu(RuntimeHealthPresentation.FromNullable(health?.CpConnected)),
            ActiveSessions = node.CurrentSessions,
            SiblingHealthyCount = eligible.Count,
            SiblingFreeCapacity = freeCap,
            LocationSafe = locationSafe && block is null,
            LocationSafetyDetail = block is null
                ? $"Доступно здоровых соседей: {eligible.Count}, свободная ёмкость: {freeCap}."
                : block,
            DrainWaitSeconds = (int)NodeCommandService.UpdateDrainWait.TotalSeconds,
            DrainPolicyText = DrainPolicyText(),
            CanEnqueue = can,
            BlockReason = block,
            Notes = new[]
            {
                "Новые подключения будут запрещены на время Drain.",
                "Существующие VPN-сеансы ожидаются до завершения или до истечения тайм-аута."
            }
        };
    }

    private static string DrainPolicyText() =>
        $"Максимальное ожидание завершения активных VPN-сеансов: {(int)NodeCommandService.UpdateDrainWait.TotalSeconds} секунд. " +
        "Если через это время активные соединения останутся, обновление продолжится и оставшиеся соединения могут быть разорваны.";
}
