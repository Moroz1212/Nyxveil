using System.Text.Json;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeAdminStatusResponseContractTests
{
    [Fact]
    public void Serializes_SnakeCase_PropertyNames()
    {
        var dto = new NodeAdminStatusResponse
        {
            NodeId = "node-1",
            LocationId = "loc-1",
            Enabled = true,
            Draining = false,
            MaintenanceMode = true,
            Healthy = true,
            Accepting = false,
            Online = true,
            LastSeenAt = new DateTime(2026, 9, 9, 12, 0, 0, DateTimeKind.Utc),
            CurrentSessions = 3,
            ReportedServerVersion = "1.3.2",
            ConfigVersion = 7,
            LifecycleState = "Active"
        };

        var json = JsonSerializer.Serialize(dto);
        using var doc = JsonDocument.Parse(json);
        var root = doc.RootElement;

        Assert.True(root.TryGetProperty("node_id", out _));
        Assert.True(root.TryGetProperty("location_id", out _));
        Assert.True(root.TryGetProperty("enabled", out _));
        Assert.True(root.TryGetProperty("draining", out _));
        Assert.True(root.TryGetProperty("maintenance_mode", out _));
        Assert.True(root.TryGetProperty("healthy", out _));
        Assert.True(root.TryGetProperty("accepting", out _));
        Assert.True(root.TryGetProperty("online", out _));
        Assert.True(root.TryGetProperty("last_seen_at", out _));
        Assert.True(root.TryGetProperty("current_sessions", out _));
        Assert.True(root.TryGetProperty("reported_server_version", out _));
        Assert.True(root.TryGetProperty("config_version", out _));
        Assert.True(root.TryGetProperty("lifecycle_state", out _));

        // PascalCase names must not appear.
        Assert.False(root.TryGetProperty("NodeId", out _));
        Assert.False(root.TryGetProperty("MaintenanceMode", out _));
        Assert.False(root.TryGetProperty("ConfigVersion", out _));
    }
}
