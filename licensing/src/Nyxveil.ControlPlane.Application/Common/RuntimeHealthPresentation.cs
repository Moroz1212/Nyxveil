using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

public enum RuntimeFlagState
{
    Unknown = 0,
    Ok = 1,
    Fail = 2
}

public enum OperatorNodeMode
{
    Unknown = 0,
    Healthy = 1,
    Degraded = 2,
    Offline = 3,
    Recovering = 4,
    Draining = 5,
    Maintenance = 6,
    Disabled = 7
}

/// <summary>
/// Presentation of runtime flags vs certificate metadata. Never treats valid cert as TLS runtime OK.
/// </summary>
public static class RuntimeHealthPresentation
{
    public static RuntimeFlagState FromNullable(bool? value) => value switch
    {
        true => RuntimeFlagState.Ok,
        false => RuntimeFlagState.Fail,
        null => RuntimeFlagState.Unknown
    };

    public static string FlagLabelRu(RuntimeFlagState state) => state switch
    {
        RuntimeFlagState.Ok => "OK",
        RuntimeFlagState.Fail => "FAIL",
        _ => "Нет данных"
    };

    public static OperatorNodeMode EvaluateMode(
        Node node,
        NodeHealth? health,
        NodeConfig? config,
        DateTime utcNow,
        NodeHeartbeatOptions? heartbeat = null,
        bool recentSuccessfulUpdate = false)
    {
        if (node.LifecycleState == NodeLifecycleState.Deleted)
            return OperatorNodeMode.Unknown;
        if (!node.Enabled)
            return OperatorNodeMode.Disabled;
        if (config?.MaintenanceMode == true)
            return OperatorNodeMode.Maintenance;
        if (node.Draining)
            return OperatorNodeMode.Draining;

        heartbeat ??= new NodeHeartbeatOptions();
        var seen = node.LastSeenAt ?? health?.UpdatedAt;
        var freshness = NodeFreshness.Evaluate(seen, utcNow, heartbeat);
        if (freshness == DataFreshness.Stale || seen is null || node.Status == NodeRuntimeStatus.Offline)
            return OperatorNodeMode.Offline;

        var runtimeFail = health is not null && (
            health.TunReady == false ||
            health.TlsOk == false ||
            health.QuicOk == false ||
            health.BridgeOk == false ||
            health.TicketKeysLoaded == false ||
            health.CpConnected == false ||
            health.Healthy == false);

        if (recentSuccessfulUpdate && runtimeFail && freshness is DataFreshness.Fresh or DataFreshness.Warning)
            return OperatorNodeMode.Recovering;

        if (runtimeFail || node.Status == NodeRuntimeStatus.Degraded || freshness == DataFreshness.Warning)
            return OperatorNodeMode.Degraded;

        if (node.Status == NodeRuntimeStatus.Healthy && health?.Healthy != false)
            return OperatorNodeMode.Healthy;

        return OperatorNodeMode.Degraded;
    }

    public static string ModeLabelRu(OperatorNodeMode mode) => mode switch
    {
        OperatorNodeMode.Healthy => "Работает штатно",
        OperatorNodeMode.Degraded => "Ограниченная работа",
        OperatorNodeMode.Offline => "Не в сети",
        OperatorNodeMode.Recovering => "Восстановление после обновления",
        OperatorNodeMode.Draining => "Завершение сеансов",
        OperatorNodeMode.Maintenance => "Обслуживание",
        OperatorNodeMode.Disabled => "Отключён",
        _ => "Неизвестно"
    };
}
