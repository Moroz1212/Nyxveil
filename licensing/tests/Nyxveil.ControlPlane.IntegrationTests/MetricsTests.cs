using System.Globalization;
using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Web.Metrics;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class MetricsTests
{
    [Fact]
    public async Task History_filters_before_aggregation_and_preserves_nulls_and_gaps()
    {
        using var connection = new SqliteConnection("Data Source=:memory:");
        await connection.OpenAsync();
        await using var db = new RelationalTestDbContext(new DbContextOptionsBuilder<RelationalTestDbContext>().UseSqlite(connection).Options);
        await db.Database.EnsureCreatedAsync();
        var until = new DateTime(2026, 9, 5, 12, 0, 0, DateTimeKind.Utc);
        db.Locations.Add(new Location { LocationId = "ru", Code = "ru", DisplayName = "Россия" });
        db.Nodes.AddRange(new Node { NodeId = "quiet", DisplayName = "Тихий", LocationId = "ru" },
            new Node { NodeId = "busy", DisplayName = "Нагруженный", LocationId = "ru" },
            new Node { NodeId = "empty", LocationId = "ru" });
        NodeMetric Sample(string node, DateTime time, double cpu, int sessions = 1, double? rx = null) =>
            new()
            {
                Id = Guid.NewGuid(),
                NodeId = node,
                Timestamp = time,
                CpuPercent = cpu,
                MemoryPercent = 20,
                ActiveSessions = sessions,
                NetworkRxRate = rx
            };
        db.NodeMetrics.AddRange(Sample("quiet", until.AddMinutes(-50), 20, 2),
            Sample("quiet", until.AddMinutes(-49), 40, 7, 10), Sample("quiet", until.AddMinutes(-15), 80),
            Sample("quiet", until.AddHours(-2), 100), Sample("quiet", until.AddMinutes(1), 99));
        for (var i = 0; i < 350; i++) db.NodeMetrics.Add(Sample("busy", until.AddSeconds(-i), 99));
        await db.SaveChangesAsync();
        db.ChangeTracker.Clear();
        var buckets = await MetricsQuery.History(db, "quiet", until.AddHours(-1), until, 15).ToListAsync();
        Assert.Equal(2, buckets.Count);
        Assert.Equal(30, buckets[0].Cpu);
        Assert.Equal(7, buckets[0].Sessions);
        Assert.Equal(10, buckets[0].Rx);
        Assert.Null(buckets[0].Tx);
        Assert.Equal(3, buckets.Sum(b => b.Samples));
        var points = MetricsQuery.Points(buckets, until.AddHours(-1), until, 15);
        Assert.Equal(5, points.Count);
        Assert.Null(points[1].Cpu);
        Assert.Null(points[1].Rx);
        Assert.Equal(0, points[1].Samples);
        Assert.Equal(80, points[3].Cpu);
        var overview = await MetricsQuery.OverviewAsync(db, until);
        Assert.Equal(3, overview.Count);
        Assert.Equal(80, overview.Single(n => n.NodeId == "quiet").Latest!.CpuPercent);
        Assert.Null(overview.Single(n => n.NodeId == "empty").Latest);
        Assert.Empty(db.ChangeTracker.Entries());
    }

    [Theory]
    [InlineData(1, 1, 61)]
    [InlineData(6, 5, 73)]
    [InlineData(24, 15, 97)]
    [InlineData(168, 60, 169)]
    public void Periods_remain_bounded_and_translate_to_sql_server(int hours, int minutes, int count)
    {
        var until = new DateTime(2026, 9, 5, 12, 0, 0, DateTimeKind.Utc);
        using var db = new ControlPlaneDbContext(new DbContextOptionsBuilder<ControlPlaneDbContext>()
            .UseSqlServer("Server=unused;Database=unused;Integrated Security=true").Options);
        Assert.Equal(minutes, MetricsQuery.BucketMinutes(hours));
        var sql = MetricsQuery.History(db, "node", until.AddHours(-hours), until, minutes).ToQueryString();
        Assert.Contains("GROUP BY", sql);
        Assert.Contains("AVG(", sql);
        Assert.Contains("[NodeId] =", sql);
        Assert.Equal(count, MetricsQuery.Points([], until.AddHours(-hours), until, minutes).Count);
    }

    [Fact]
    public void Sorting_is_numeric_stable_and_missing_values_are_last_in_both_directions()
    {
        ServerMetricRow Row(string id, double? cpu) => new(id, id, "ru", NodeRuntimeStatus.Healthy,
            cpu.HasValue ? new NodeMetric { CpuPercent = cpu.Value } : null);
        var rows = new[] { Row("missing", null), Row("low", 2), Row("high", 100), Row("middle", 20) };
        Assert.Equal(["low", "middle", "high", "missing"], MetricsQuery.Sort(rows, "cpu", false).Select(n => n.NodeId));
        Assert.Equal(["high", "middle", "low", "missing"], MetricsQuery.Sort(rows, "cpu", true).Select(n => n.NodeId));
    }

    [Fact]
    public void Chart_coordinates_are_culture_independent_and_do_not_bridge_missing_samples()
    {
        var old = CultureInfo.CurrentCulture;
        try
        {
            CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("ru-RU");
            var from = new DateTime(2026, 9, 5, 0, 0, 0, DateTimeKind.Utc);
            MetricPoint Point(int minute, double? value) => new(from.AddMinutes(minute), value, null, null, null, null, 1);
            var chart = ChartGeometry.Create([Point(0, 50.5), Point(1, null), Point(2, 75), Point(3, double.NaN)],
                p => p.Cpu, from, from.AddMinutes(4), 100);
            Assert.Equal(2, chart.Dots.Count);
            Assert.Equal(2, chart.Path.Split(" M ").Length - 1);
            Assert.DoesNotContain(" L ", chart.Path);
            Assert.DoesNotContain(",", chart.Path);
            Assert.Equal(334, chart.Dots[1].X);
        }
        finally { CultureInfo.CurrentCulture = old; }
    }
}
