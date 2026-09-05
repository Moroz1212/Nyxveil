using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Web.Presentation;

namespace Nyxveil.ControlPlane.Web.Metrics;

public sealed record ServerMetricRow(string NodeId, string Name, string Location,
    NodeRuntimeStatus Status, NodeMetric? Latest);
public sealed record MetricPoint(DateTime Time, double? Cpu, double? Memory, double? Sessions,
    double? Rx, double? Tx, int Samples);
public sealed class MetricBucket
{
    public int Year { get; set; }
    public int Month { get; set; }
    public int Day { get; set; }
    public int Hour { get; set; }
    public int Minute { get; set; }
    public double Cpu { get; set; }
    public double Memory { get; set; }
    public int Sessions { get; set; }
    public double? Rx { get; set; }
    public double? Tx { get; set; }
    public int Samples { get; set; }
}

public static class MetricsQuery
{
    public static int BucketMinutes(int hours) => hours switch
    {
        1 => 1,
        6 => 5,
        24 => 15,
        168 => 60,
        _ => throw new ArgumentOutOfRangeException(nameof(hours))
    };
    // Filter by node and period before grouping: at most 169 result buckets, no global sample limit.
    public static IQueryable<MetricBucket> History(ControlPlaneDbContext db, string nodeId,
        DateTime from, DateTime until, int minutes) => db.NodeMetrics.AsNoTracking()
        .Where(m => m.NodeId == nodeId && m.Timestamp >= from && m.Timestamp <= until)
        .GroupBy(m => new
        {
            m.Timestamp.Year,
            m.Timestamp.Month,
            m.Timestamp.Day,
            m.Timestamp.Hour,
            Minute = m.Timestamp.Minute / minutes
        })
        .Select(g => new MetricBucket
        {
            Year = g.Key.Year,
            Month = g.Key.Month,
            Day = g.Key.Day,
            Hour = g.Key.Hour,
            Minute = g.Key.Minute * minutes,
            Cpu = g.Average(m => m.CpuPercent),
            Memory = g.Average(m => m.MemoryPercent),
            Sessions = g.Max(m => m.ActiveSessions),
            Rx = g.Average(m => m.NetworkRxRate),
            Tx = g.Average(m => m.NetworkTxRate),
            Samples = g.Count()
        }).OrderBy(m => m.Year).ThenBy(m => m.Month).ThenBy(m => m.Day).ThenBy(m => m.Hour).ThenBy(m => m.Minute);

    public static async Task<List<ServerMetricRow>> OverviewAsync(ControlPlaneDbContext db,
        DateTime until, CancellationToken cancellationToken = default)
    {
        // Correlated TOP(1): quiet servers cannot disappear behind busy ones.
        var rows = await db.Nodes.AsNoTracking().Select(n => new
        {
            n.NodeId,
            n.DisplayName,
            n.LocationId,
            n.Status,
            Latest = db.NodeMetrics.Where(m => m.NodeId == n.NodeId && m.Timestamp <= until)
                .OrderByDescending(m => m.Timestamp).ThenByDescending(m => m.Id).FirstOrDefault()
        }).ToListAsync(cancellationToken);
        return rows.Select(n => new ServerMetricRow(n.NodeId,
            string.IsNullOrWhiteSpace(n.DisplayName) ? n.NodeId : n.DisplayName,
            n.LocationId, n.Status, n.Latest)).ToList();
    }
    public static IEnumerable<ServerMetricRow> Sort(IEnumerable<ServerMetricRow> source, string column, bool descending)
    {
        if (column is "name" or "location" or "status")
        {
            Func<ServerMetricRow, string> key = column switch
            {
                "location" => r => r.Location,
                "status" => r => UiText.Status(r.Status.ToString()),
                _ => r => r.Name
            };
            var comparer = StringComparer.Create(UiText.Culture, ignoreCase: true);
            return (descending ? source.OrderByDescending(key, comparer) : source.OrderBy(key, comparer))
                .ThenBy(r => r.NodeId, StringComparer.Ordinal);
        }
        Func<ServerMetricRow, double?> number = column switch
        {
            "cpu" => r => r.Latest?.CpuPercent,
            "memory" => r => r.Latest?.MemoryPercent,
            "sessions" => r => r.Latest?.ActiveSessions,
            "rx" => r => r.Latest?.NetworkRxRate,
            "tx" => r => r.Latest?.NetworkTxRate,
            "time" => r => r.Latest?.Timestamp.Ticks,
            _ => throw new ArgumentException("Unknown sort column", nameof(column))
        };
        double? Valid(ServerMetricRow r) => number(r) is { } v && double.IsFinite(v) ? v : null;
        var presentFirst = source.OrderBy(r => !Valid(r).HasValue);
        return (descending ? presentFirst.ThenByDescending(Valid) : presentFirst.ThenBy(Valid))
            .ThenBy(r => r.Name, StringComparer.Create(UiText.Culture, true)).ThenBy(r => r.NodeId, StringComparer.Ordinal);
    }
    public static List<MetricPoint> Points(IEnumerable<MetricBucket> buckets, DateTime from, DateTime until, int minutes)
    {
        var byTime = buckets.ToDictionary(b => new DateTime(b.Year, b.Month, b.Day, b.Hour, b.Minute, 0, DateTimeKind.Utc));
        var start = new DateTime(from.Year, from.Month, from.Day, from.Hour, from.Minute / minutes * minutes, 0, DateTimeKind.Utc);
        var points = new List<MetricPoint>();
        for (var time = start; time <= until; time = time.AddMinutes(minutes))
        {
            var b = byTime.GetValueOrDefault(time);
            points.Add(new MetricPoint(time < from ? from : time, b?.Cpu, b?.Memory, b?.Sessions,
                b?.Rx, b?.Tx, b?.Samples ?? 0));
        }
        return points;
    }
}
