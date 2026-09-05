using Nyxveil.ControlPlane.Application.Tickets;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class TicketScopeCalculatorTests
{
    [Fact]
    public void TestTicketRefreshNeverWidens()
    {
        var result = TicketScopeCalculator.RefreshLocations(
            new[] { "ams", "fra" },
            new[] { "ams" });

        Assert.Equal(new[] { "ams" }, result);

        // Unrestricted license keep old locations (does not invent new ones).
        var preserved = TicketScopeCalculator.RefreshLocations(
            new[] { "ams" },
            Array.Empty<string>());
        Assert.Equal(new[] { "ams" }, preserved);

        Assert.False(TicketScopeCalculator.TryRefreshLocations(
            new[] { "ams" },
            new[] { "fra" },
            out _,
            out var error));
        Assert.Contains("no allowed locations", error);
    }

    [Fact]
    public void TestTicketRefreshLicenseDowngrade()
    {
        Assert.True(TicketScopeCalculator.TryComputeRefreshScope(
            currentPlan: "standard",
            oldLocations: new[] { "ams", "fra" },
            currentAllowedLocations: new[] { "ams" },
            oldNodeScope: new[] { "node-a", "node-b" },
            allowedNodes: new[] { "node-a" },
            out var role,
            out var permissions,
            out var locations,
            out var nodeScope,
            out var error));

        Assert.Null(error);
        Assert.Equal("user", role);
        Assert.Equal(new[] { TicketScopeCalculator.PermissionConnect }, permissions);
        Assert.Equal(new[] { "ams" }, locations);
        Assert.Equal(new[] { "node-a" }, nodeScope);

        Assert.Equal("master", TicketScopeCalculator.RoleForPlan("master"));
        Assert.Equal("user", TicketScopeCalculator.RoleForPlan("standard"));
    }

    [Fact]
    public void TestTicketRefreshPreservesNodeScope_WhenAllowedNodesEmpty()
    {
        var result = TicketScopeCalculator.RefreshNodeScope(
            new[] { "node-a", "node-b" },
            Array.Empty<string>());

        Assert.Equal(new[] { "node-a", "node-b" }, result);
    }

    [Fact]
    public void TestTicketRefreshRejectsEmptyIntersection()
    {
        Assert.Throws<TicketScopeRejectedException>(() =>
            TicketScopeCalculator.RefreshNodeScope(
                new[] { "node-a" },
                new[] { "node-b" }));

        Assert.False(TicketScopeCalculator.TryRefreshNodeScope(
            new[] { "node-a" },
            new[] { "node-b" },
            out var result,
            out var error));
        Assert.Empty(result);
        Assert.Contains("empty node scope", error);
    }

    [Fact]
    public void TestLocationScopedRefreshRemainsLocationScoped()
    {
        var nodeScope = TicketScopeCalculator.RefreshNodeScope(
            Array.Empty<string>(),
            new[] { "node-a", "node-b" });

        Assert.Empty(nodeScope);

        Assert.True(TicketScopeCalculator.TryComputeRefreshScope(
            "standard",
            oldLocations: new[] { "ams" },
            currentAllowedLocations: new[] { "ams", "fra" },
            oldNodeScope: Array.Empty<string>(),
            allowedNodes: new[] { "node-a" },
            out _,
            out _,
            out var locations,
            out var refreshedNodes,
            out var error));

        Assert.Null(error);
        Assert.Equal(new[] { "ams" }, locations);
        Assert.Empty(refreshedNodes);
    }

    [Fact]
    public void Intersect_PreservesOrderOfFirstOperand()
    {
        var intersection = TicketScopeCalculator.Intersect(
            new[] { "c", "a", "b" },
            new[] { "b", "a" });

        Assert.Equal(new[] { "a", "b" }, intersection);
    }
}
