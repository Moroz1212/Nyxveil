using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class AdminPanelSafetyTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task DecommissionPreviewReportsSessionsEndpointsAndMetrics()
    {
        var nodeId = await RegisterAsync("admin-preview");
        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest { NodeId = nodeId, CurrentSessions = 3 });

        var preview = await _fx.NodeManagement.GetDecommissionPreviewAsync(nodeId);
        Assert.Equal(3, preview.CurrentSessions);
        Assert.Equal(1, preview.EndpointCount);
        Assert.True(preview.MetricCount >= 1);
        Assert.Contains("drained", preview.ImpactSummary);
    }

    [Fact]
    public async Task DeletedNodeRejectsOrdinaryAdminMutation()
    {
        var nodeId = await RegisterAsync("admin-immutable");
        await _fx.NodeManagement.SoftDeleteAsync(nodeId, "operator", "retired");

        await Assert.ThrowsAsync<ForbiddenException>(
            () => _fx.NodeManagement.SetEnabledAsync(nodeId, true, "operator"));
        Assert.False((await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId)).Enabled);
    }

    private async Task<string> RegisterAsync(string nodeId)
    {
        var bootstrap = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 1,
            CreatedBy = "test"
        });
        await _fx.Nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = bootstrap.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = ControlPlaneTestFixture.RandomKey32(),
            Capacity = 10,
            ProtocolVersion = 1,
            Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Enabled = true }]
        });
        return nodeId;
    }
}
