using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

public static class NodeVersionEvaluator
{
    public static NodeVersionStatus Evaluate(
        string? installedRaw,
        string? latestRaw,
        string? minimumSupportedRaw = null)
    {
        if (string.IsNullOrWhiteSpace(installedRaw))
            return NodeVersionStatus.Unknown;

        if (!SemVersion.TryParse(installedRaw, out var installed))
            return NodeVersionStatus.Invalid;

        if (!string.IsNullOrWhiteSpace(minimumSupportedRaw) &&
            SemVersion.TryParse(minimumSupportedRaw, out var min) &&
            installed < min)
            return NodeVersionStatus.Unsupported;

        if (string.IsNullOrWhiteSpace(latestRaw))
            return NodeVersionStatus.Unknown;

        if (!SemVersion.TryParse(latestRaw, out var latest))
            return NodeVersionStatus.Unknown;

        var cmp = installed.CompareTo(latest);
        if (cmp == 0) return NodeVersionStatus.Current;
        if (cmp < 0) return NodeVersionStatus.UpdateAvailable;
        return NodeVersionStatus.Ahead;
    }

    public static string? EffectiveInstalledVersion(string? reported, string? registered) =>
        !string.IsNullOrWhiteSpace(reported) ? reported.Trim() :
        !string.IsNullOrWhiteSpace(registered) ? registered.Trim() : null;

    public static string DisplayRu(NodeVersionStatus status) => status switch
    {
        NodeVersionStatus.Current => "Актуально",
        NodeVersionStatus.UpdateAvailable => "Доступно обновление",
        NodeVersionStatus.Unsupported => "Требуется обновление",
        NodeVersionStatus.Ahead => "Новее актуальной",
        NodeVersionStatus.Unknown => "Версия неизвестна",
        NodeVersionStatus.Invalid => "Некорректная версия",
        NodeVersionStatus.Updating => "Обновление…",
        NodeVersionStatus.UpdateFailed => "Ошибка обновления",
        _ => status.ToString()
    };
}
