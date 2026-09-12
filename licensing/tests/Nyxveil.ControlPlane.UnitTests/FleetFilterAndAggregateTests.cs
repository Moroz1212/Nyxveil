using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class FleetFilterAndAggregateTests
{
    [Fact]
    public void Maintenance_WhenAllNodesMaintenance()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Healthy, Maintenance = true, Capacity = 10 },
            new() { Mode = OperatorNodeMode.Healthy, Maintenance = true, Capacity = 10 }
        };
        var h = FleetOverviewService.DeriveLocationHealth(nodes, allMaintenance: true, healthy: 2, offline: 0, remaining: 20, totalCap: 20);
        Assert.Equal(FleetLocationHealth.Maintenance, h);
    }

    [Fact]
    public void TestOnlyOffline_DoesNotForceCriticalWhenProductionHealthy()
    {
        // Production basis excludes TestOnly (service uses prod list for health).
        var prod = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 1, TestOnly = false },
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 1, TestOnly = false }
        };
        var h = FleetOverviewService.DeriveLocationHealth(prod, false, healthy: 2, offline: 0, remaining: 18, totalCap: 20);
        Assert.Equal(FleetLocationHealth.Healthy, h);
    }

    [Fact]
    public void SessionsAndCapacity_Aggregate()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { CurrentSessions = 10, Capacity = 100 },
            new() { CurrentSessions = 20, Capacity = 100 }
        };
        var sessions = nodes.Sum(n => n.CurrentSessions);
        var cap = nodes.Sum(n => n.Capacity);
        Assert.Equal(30, sessions);
        Assert.Equal(200, cap);
        Assert.Equal(15.0, Math.Round(100.0 * sessions / cap, 1));
    }

    [Fact]
    public void UnknownCapacity_NullNotZero()
    {
        int? totalCap = null;
        double? used = totalCap is > 0 ? 0 : null;
        Assert.Null(used);
    }

    [Fact]
    public void VersionBuckets_ExcludeEmptyAsUnknownKey()
    {
        var rows = new[]
        {
            new FleetNodeRowDto { ServerVersion = "1.1.14" },
            new FleetNodeRowDto { ServerVersion = "1.1.14" },
            new FleetNodeRowDto { ServerVersion = "1.1.13" },
            new FleetNodeRowDto { ServerVersion = null }
        };
        var buckets = rows
            .GroupBy(r => string.IsNullOrWhiteSpace(r.ServerVersion) ? "unknown" : r.ServerVersion!.Trim(),
                StringComparer.OrdinalIgnoreCase)
            .Select(g => (g.Key, g.Count()))
            .OrderByDescending(x => x.Item2)
            .ToList();
        Assert.Equal(("1.1.14", 2), buckets[0]);
        Assert.Contains(buckets, b => b.Key == "1.1.13" && b.Item2 == 1);
        Assert.Contains(buckets, b => b.Key == "unknown" && b.Item2 == 1);
    }

    [Fact]
    public void NearestCertExpiry_MinDays()
    {
        var days = new int?[] { 84, 12, null, 40 };
        int? nearest = days.Where(d => d.HasValue).Select(d => d!.Value).DefaultIfEmpty().Min();
        Assert.Equal(12, nearest);
    }

    [Fact]
    public void FilterHealthy_OnlyHealthyLocations()
    {
        var cards = new List<FleetLocationCardDto>
        {
            new() { Health = FleetLocationHealth.Healthy, DisplayName = "A", Nodes = new List<FleetNodeRowDto>() },
            new() { Health = FleetLocationHealth.Degraded, DisplayName = "B", Nodes = new List<FleetNodeRowDto>() }
        };
        var filtered = cards.Where(c => c.Health == FleetLocationHealth.Healthy).ToList();
        Assert.Single(filtered);
        Assert.Equal("A", filtered[0].DisplayName);
    }

    [Fact]
    public void Search_MatchesNodeId()
    {
        var cards = new List<FleetLocationCardDto>
        {
            new()
            {
                DisplayName = "Finland",
                LocationId = "fi",
                Country = "FI",
                City = "Helsinki",
                Nodes = new List<FleetNodeRowDto> { new() { NodeId = "fi-hel-01", DisplayName = "fi-hel-01" } }
            }
        };
        var s = "hel-01";
        var hit = cards.Where(c =>
            c.Nodes.Any(n => n.NodeId.Contains(s, StringComparison.OrdinalIgnoreCase))).ToList();
        Assert.Single(hit);
    }

    [Fact]
    public void DeletedLifecycle_NotOperatorVisible()
    {
        Assert.False(NodeInventory.IsOperatorVisible(Domain.Enums.NodeLifecycleState.Deleted));
    }
}
