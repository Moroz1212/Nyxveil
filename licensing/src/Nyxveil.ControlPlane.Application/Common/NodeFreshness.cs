using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Application.Common;

public enum DataFreshness
{
    Unknown = 0,
    Fresh = 1,
    Warning = 2,
    Stale = 3
}

/// <summary>
/// Heartbeat / health snapshot age aligned with <see cref="NodeHeartbeatOptions"/>.
/// Fresh &lt; DegradedAfter; Warning until OfflineAfter; then Stale.
/// </summary>
public static class NodeFreshness
{
    public static DataFreshness Evaluate(DateTime? updatedAtUtc, DateTime utcNow, NodeHeartbeatOptions? options = null)
    {
        if (updatedAtUtc is null)
            return DataFreshness.Unknown;

        options ??= new NodeHeartbeatOptions();
        var age = utcNow - updatedAtUtc.Value;
        if (age < TimeSpan.Zero)
            age = TimeSpan.Zero;

        if (age.TotalSeconds < options.DegradedAfterSeconds)
            return DataFreshness.Fresh;
        if (age.TotalSeconds < options.OfflineAfterSeconds)
            return DataFreshness.Warning;
        return DataFreshness.Stale;
    }

    public static TimeSpan? Age(DateTime? updatedAtUtc, DateTime utcNow) =>
        updatedAtUtc is null ? null : (utcNow - updatedAtUtc.Value < TimeSpan.Zero
            ? TimeSpan.Zero
            : utcNow - updatedAtUtc.Value);

    public static string FormatAgeRu(TimeSpan? age)
    {
        if (age is null) return "нет данных";
        if (age.Value.TotalSeconds < 60)
            return $"{(int)age.Value.TotalSeconds} сек. назад";
        if (age.Value.TotalMinutes < 60)
            return $"{(int)age.Value.TotalMinutes} мин. назад";
        if (age.Value.TotalHours < 48)
            return $"{(int)age.Value.TotalHours} ч. назад";
        return $"{(int)age.Value.TotalDays} дн. назад";
    }

    public static string LabelRu(DataFreshness freshness, TimeSpan? age) => freshness switch
    {
        DataFreshness.Fresh => $"Последние данные: {FormatAgeRu(age)}",
        DataFreshness.Warning => $"Данные устаревают: {FormatAgeRu(age)}",
        DataFreshness.Stale => $"Данные устарели: {FormatAgeRu(age)}",
        _ => "Нет данных о свежести"
    };
}
