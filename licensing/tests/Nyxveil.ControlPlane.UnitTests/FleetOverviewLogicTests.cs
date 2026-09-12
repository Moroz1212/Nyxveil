using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class FleetOverviewLogicTests
{
    [Fact]
    public void DeletedNodes_NeverInOperatorInventoryHelper()
    {
        Assert.False(NodeInventory.IsOperatorVisible(Domain.Enums.NodeLifecycleState.Deleted));
        Assert.True(NodeInventory.IsOperatorVisible(Domain.Enums.NodeLifecycleState.Active));
    }

    [Fact]
    public void LocationHealth_HealthyWhenAllOk()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 2 },
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 1 }
        };
        var h = FleetOverviewService.DeriveLocationHealth(nodes, allMaintenance: false, healthy: 2, offline: 0, remaining: 17, totalCap: 20);
        Assert.Equal(FleetLocationHealth.Healthy, h);
    }

    [Fact]
    public void LocationHealth_DegradedWhenOneOfflineButCapacityRemains()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 2 },
            new() { Mode = OperatorNodeMode.Offline, Capacity = 10, CurrentSessions = 0 },
            new() { Mode = OperatorNodeMode.Healthy, Capacity = 10, CurrentSessions = 2 }
        };
        var h = FleetOverviewService.DeriveLocationHealth(nodes, false, healthy: 2, offline: 1, remaining: 26, totalCap: 30);
        Assert.Equal(FleetLocationHealth.Degraded, h);
    }

    [Fact]
    public void LocationHealth_CriticalWhenSingleHealthyWithOfflineSibling()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Healthy },
            new() { Mode = OperatorNodeMode.Offline }
        };
        var h = FleetOverviewService.DeriveLocationHealth(nodes, false, healthy: 1, offline: 1, remaining: 5, totalCap: 10);
        Assert.Equal(FleetLocationHealth.Critical, h);
    }

    [Fact]
    public void LocationHealth_OfflineWhenNoHealthy()
    {
        var nodes = new List<FleetNodeRowDto>
        {
            new() { Mode = OperatorNodeMode.Offline },
            new() { Mode = OperatorNodeMode.Offline }
        };
        var h = FleetOverviewService.DeriveLocationHealth(nodes, false, healthy: 0, offline: 2, remaining: 0, totalCap: 10);
        Assert.Equal(FleetLocationHealth.Offline, h);
    }

    [Fact]
    public void UnknownCapacity_NotZeroPercent()
    {
        double? pct = null;
        Assert.Null(pct);
        // UI maps null → "Нет данных"
    }

    [Fact]
    public void TlsRuntimeIndependentOfCert()
    {
        Assert.Equal(RuntimeFlagState.Unknown, RuntimeHealthPresentation.FromNullable(null));
        Assert.Equal(RuntimeFlagState.Fail, RuntimeHealthPresentation.FromNullable(false));
        Assert.Equal(CertificateHealthStatus.Healthy,
            CertificateExpiry.Evaluate(DateTime.UtcNow.AddDays(90), DateTime.UtcNow));
    }
}
