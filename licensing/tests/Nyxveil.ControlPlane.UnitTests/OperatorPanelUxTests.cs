using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class OperatorPanelPresentationTests
{
    [Fact]
    public void TlsRuntime_Unknown_DoesNotBecomeOk_WhenCertValid()
    {
        Assert.Equal(RuntimeFlagState.Unknown, RuntimeHealthPresentation.FromNullable(null));
        Assert.Equal("Нет данных", RuntimeHealthPresentation.FlagLabelRu(RuntimeFlagState.Unknown));
    }

    [Fact]
    public void CertificateValid_TlsFail_AreIndependent()
    {
        var now = DateTime.UtcNow;
        var cert = CertificateExpiry.Evaluate(now.AddDays(60), now);
        Assert.Equal(CertificateHealthStatus.Healthy, cert);
        Assert.Equal(RuntimeFlagState.Fail, RuntimeHealthPresentation.FromNullable(false));
    }

    [Fact]
    public void Recovering_AfterRecentUpdate_WhenRuntimeFailing()
    {
        var now = DateTime.UtcNow;
        var node = new Node
        {
            NodeId = "n1",
            Enabled = true,
            Status = NodeRuntimeStatus.Degraded,
            LifecycleState = NodeLifecycleState.Active,
            LastSeenAt = now
        };
        var health = new NodeHealth
        {
            NodeId = "n1",
            Healthy = false,
            TlsOk = false,
            QuicOk = false,
            TunReady = true,
            CpConnected = true,
            UpdatedAt = now
        };
        var mode = RuntimeHealthPresentation.EvaluateMode(node, health, null, now, recentSuccessfulUpdate: true);
        Assert.Equal(OperatorNodeMode.Recovering, mode);
    }

    [Fact]
    public void Freshness_UsesHeartbeatOptionsThresholds()
    {
        var now = DateTime.UtcNow;
        var opts = new NodeHeartbeatOptions { DegradedAfterSeconds = 90, OfflineAfterSeconds = 180 };
        Assert.Equal(DataFreshness.Fresh, NodeFreshness.Evaluate(now.AddSeconds(-30), now, opts));
        Assert.Equal(DataFreshness.Warning, NodeFreshness.Evaluate(now.AddSeconds(-120), now, opts));
        Assert.Equal(DataFreshness.Stale, NodeFreshness.Evaluate(now.AddSeconds(-200), now, opts));
        Assert.Equal(DataFreshness.Unknown, NodeFreshness.Evaluate(null, now, opts));
    }

    [Fact]
    public void UpdateTimeline_UsesOnlyRealTimestamps()
    {
        var cmd = new NodeCommand
        {
            Type = NodeCommandType.UpdateNodeLatest,
            CreatedAt = DateTime.UtcNow.AddMinutes(-5),
            IssuedAt = DateTime.UtcNow.AddMinutes(-5),
            ClaimedAt = DateTime.UtcNow.AddMinutes(-4),
            StartedAt = DateTime.UtcNow.AddMinutes(-3),
            CompletedAt = DateTime.UtcNow.AddMinutes(-1),
            Status = NodeCommandStatus.Succeeded,
            ResultCode = "updated_healthy",
            ProgressPhase = "Completed"
        };
        var steps = UpdateCommandTimeline.Build(cmd);
        Assert.Contains(steps, s => s.Id == "created" && s.Reached);
        Assert.Contains(steps, s => s.Id == "terminal" && s.Reached);
        Assert.DoesNotContain(steps, s => s.Title.Contains("выдуман", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public void NodeInventory_ExcludesDeleted()
    {
        var nodes = new[]
        {
            new Node { NodeId = "a", LifecycleState = NodeLifecycleState.Active },
            new Node { NodeId = "b", LifecycleState = NodeLifecycleState.Deleted },
            new Node { NodeId = "c", LifecycleState = NodeLifecycleState.Revoked }
        };
        var visible = NodeInventory.OperatorVisible(nodes).Select(n => n.NodeId).ToArray();
        Assert.Equal(new[] { "a", "c" }, visible);
    }
}

public sealed class DeletedNodeOperatorVisibilityTests
{
    private static ServiceProvider Build(string name)
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddSingleton<IServerReleaseService>(new FakeServerReleaseService());
        services.AddDbContextFactory<ControlPlaneDbContext>(o => o.UseInMemoryDatabase(name));
        services.AddScoped<IDashboardQueryService, DashboardQueryService>();
        return services.BuildServiceProvider();
    }

    [Fact]
    public async Task Dashboard_ExcludesDeletedFromActiveCounts_AndAttention()
    {
        await using var sp = Build("del-dash-" + Guid.NewGuid().ToString("N"));
        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            await db.Database.EnsureCreatedAsync();
            db.Nodes.Add(new Node
            {
                NodeId = "alive",
                DisplayName = "Alive",
                LocationId = "fi",
                LifecycleState = NodeLifecycleState.Active,
                Status = NodeRuntimeStatus.Healthy,
                Enabled = true,
                LastSeenAt = DateTime.UtcNow,
                Capacity = 10
            });
            db.Nodes.Add(new Node
            {
                NodeId = "gone",
                DisplayName = "Gone",
                LocationId = "fi",
                LifecycleState = NodeLifecycleState.Deleted,
                Status = NodeRuntimeStatus.Offline,
                Enabled = false,
                CertNotAfter = DateTime.UtcNow.AddDays(-1),
                Capacity = 10
            });
            await db.SaveChangesAsync();
        }

        var dash = sp.GetRequiredService<IDashboardQueryService>();
        var summary = await dash.GetSummaryAsync();
        Assert.Equal(1, summary.TotalNodes);
        Assert.Equal(1, summary.DeletedNodes);
        Assert.Equal(0, summary.CertificatesExpired); // deleted cert must not count

        var attention = await dash.GetAttentionAsync();
        Assert.DoesNotContain(attention, a => a.NodeId == "gone");
    }
}

public sealed class UpdatePreflightServiceTests
{
    [Fact]
    public async Task Preflight_Blocked_WhenNoHealthySibling()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddSingleton<IServerReleaseService>(new FakeServerReleaseService());
        services.AddDbContextFactory<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase("preflight-" + Guid.NewGuid().ToString("N")));
        services.AddScoped<IUpdatePreflightService, UpdatePreflightService>();
        await using var sp = services.BuildServiceProvider();

        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            await db.Database.EnsureCreatedAsync();
            db.Nodes.Add(new Node
            {
                NodeId = "solo",
                DisplayName = "Solo",
                LocationId = "fi",
                LifecycleState = NodeLifecycleState.Active,
                Status = NodeRuntimeStatus.Healthy,
                Enabled = true,
                LastSeenAt = DateTime.UtcNow,
                Capacity = 10,
                CurrentSessions = 2,
                ReportedServerVersion = "1.1.9",
                SupportsNodeCommands = true,
                ManagementCapabilities = "node_update,service_restart,host_reboot,certificate_renew"
            });
            await db.SaveChangesAsync();
        }

        var pre = await sp.GetRequiredService<IUpdatePreflightService>().EvaluateUpdateAsync("solo");
        Assert.False(pre.CanEnqueue);
        Assert.False(pre.LocationSafe);
        Assert.Equal(2, pre.ActiveSessions);
        Assert.Equal(120, pre.DrainWaitSeconds);
        Assert.Contains("120", pre.DrainPolicyText);
    }

    [Fact]
    public async Task Preflight_DeletedNode_NotFound()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddSingleton<IServerReleaseService>(new FakeServerReleaseService());
        services.AddDbContextFactory<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase("preflight-del-" + Guid.NewGuid().ToString("N")));
        services.AddScoped<IUpdatePreflightService, UpdatePreflightService>();
        await using var sp = services.BuildServiceProvider();
        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            await db.Database.EnsureCreatedAsync();
            db.Nodes.Add(new Node
            {
                NodeId = "gone",
                LocationId = "fi",
                LifecycleState = NodeLifecycleState.Deleted,
                Capacity = 1
            });
            await db.SaveChangesAsync();
        }

        var pre = await sp.GetRequiredService<IUpdatePreflightService>().EvaluateUpdateAsync("gone");
        Assert.False(pre.CanEnqueue);
        Assert.Equal("Сервер не найден.", pre.BlockReason);
    }
}
