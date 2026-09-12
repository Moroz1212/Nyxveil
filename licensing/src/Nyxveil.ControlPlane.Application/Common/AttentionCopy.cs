using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>
/// Dashboard Attention copy and command-visibility policy.
/// Russian literals use Unicode escapes so Release compiles cannot
/// mojibake them when the host ANSI code page is CP1251.
/// </summary>
public static class AttentionCopy
{
    // "Требует внимания"
    public const string PanelTitle = "\u0422\u0440\u0435\u0431\u0443\u0435\u0442 \u0432\u043d\u0438\u043c\u0430\u043d\u0438\u044f";

    // "Сервер не в сети"
    public const string ServerOffline = "\u0421\u0435\u0440\u0432\u0435\u0440 \u043d\u0435 \u0432 \u0441\u0435\u0442\u0438";

    // "Нет свежего heartbeat."
    public const string NoFreshHeartbeat = "\u041d\u0435\u0442 \u0441\u0432\u0435\u0436\u0435\u0433\u043e heartbeat.";

    // "Ограниченная работа"
    public const string Degraded = "\u041e\u0433\u0440\u0430\u043d\u0438\u0447\u0435\u043d\u043d\u0430\u044f \u0440\u0430\u0431\u043e\u0442\u0430";

    // "Нет связи с Control Plane"
    public const string CpDisconnected = "\u041d\u0435\u0442 \u0441\u0432\u044f\u0437\u0438 \u0441 Control Plane";

    // "Сертификат истёк"
    public const string CertExpired = "\u0421\u0435\u0440\u0442\u0438\u0444\u0438\u043a\u0430\u0442 \u0438\u0441\u0442\u0451\u043a";

    // "Требуется обновление TLS-сертификата."
    public const string CertExpiredDetail =
        "\u0422\u0440\u0435\u0431\u0443\u0435\u0442\u0441\u044f \u043e\u0431\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u0435 TLS-\u0441\u0435\u0440\u0442\u0438\u0444\u0438\u043a\u0430\u0442\u0430.";

    // "Операция завершилась с ошибкой"
    public const string OperationFailed =
        "\u041e\u043f\u0435\u0440\u0430\u0446\u0438\u044f \u0437\u0430\u0432\u0435\u0440\u0448\u0438\u043b\u0430\u0441\u044c \u0441 \u043e\u0448\u0438\u0431\u043a\u043e\u0439";

    // "Неопределённый результат обновления"
    public const string UnknownUpdateOutcome =
        "\u041d\u0435\u043e\u043f\u0440\u0435\u0434\u0435\u043b\u0451\u043d\u043d\u044b\u0439 \u0440\u0435\u0437\u0443\u043b\u044c\u0442\u0430\u0442 \u043e\u0431\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u044f";

    // "Нет данных"
    public const string NoData = "\u041d\u0435\u0442 \u0434\u0430\u043d\u043d\u044b\u0445";

    // "Обновление сертификата" (must match UiText.CommandType)
    public const string RenewCertificate =
        "\u041e\u0431\u043d\u043e\u0432\u043b\u0435\u043d\u0438\u0435 \u0441\u0435\u0440\u0442\u0438\u0444\u0438\u043a\u0430\u0442\u0430";
}

/// <summary>
/// Which terminal NodeCommands remain actionable Dashboard Attention items.
/// History stays in Operations/Audit; Attention is current unresolved risk.
/// </summary>
public static class AttentionCommandPolicy
{
    private static readonly HashSet<string> UnknownOutcomes = new(StringComparer.OrdinalIgnoreCase)
    {
        "expired_outcome_unknown",
        "outcome_unknown",
        "rollback_failed"
    };

    /// <summary>
    /// Terminal outcomes that are not current incidents (resolved / expected).
    /// </summary>
    private static readonly HashSet<string> NonActionableCodes = new(StringComparer.OrdinalIgnoreCase)
    {
        "rolled_back_healthy",
        "updated_healthy",
        "renewed",
        "success",
        "ok",
        "already_fresh",
        "expired" // plain TTL expiry without unknown outcome
    };

    public static bool IsUnknownOutcome(string? resultCode) =>
        !string.IsNullOrWhiteSpace(resultCode) && UnknownOutcomes.Contains(resultCode);

    public static bool IsActionableAttentionCommand(
        NodeCommandStatus status,
        string? resultCode,
        DateTime? completedAt,
        DateTime utcNow)
    {
        if (completedAt is null)
            return false;

        // Keep a bounded look-back for historical noise, but resolution beats age.
        if (completedAt.Value < utcNow.AddDays(-7))
            return false;

        if (IsUnknownOutcome(resultCode))
            return true;

        if (!string.IsNullOrWhiteSpace(resultCode) && NonActionableCodes.Contains(resultCode))
            return false;

        // Plain Expired without unknown outcome is historical, not current Attention.
        if (status == NodeCommandStatus.Expired)
            return false;

        return status == NodeCommandStatus.Failed;
    }
}
