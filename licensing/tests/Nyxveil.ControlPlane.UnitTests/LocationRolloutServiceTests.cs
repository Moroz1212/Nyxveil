using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class LocationRolloutServiceTests
{
    private static async Task<(ServiceProvider Sp, Node A, Node B)> SeedTwoHealthyAsync(string dbName)
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddSingleton<IServerReleaseService>(new FakeServerReleaseService());
        services.AddSingleton<IAdminRealtimeNotifier, NullAdminRealtimeNotifier>();
        services.AddDbContextFactory<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase(dbName)
                .ConfigureWarnings(w => w.Ignore(
                    Microsoft.EntityFrameworkCore.Diagnostics.InMemoryEventId.TransactionIgnoredWarning)));
        services.AddScoped<IAuditService, RecordingAudit>();
        services.AddScoped<INodeCommandService, NodeCommandService>();
        services.AddScoped<ILocationRolloutService, LocationRolloutService>();
        var sp = services.BuildServiceProvider();

        Node a, b;
        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            await db.Database.EnsureCreatedAsync();
            a = MakeNode("n-a", "A");
            b = MakeNode("n-b", "B");
            db.Nodes.AddRange(a, b);
            db.NodeConfigs.AddRange(
                new NodeConfig { NodeId = a.NodeId, Capacity = 10, ConfigVersion = 1 },
                new NodeConfig { NodeId = b.NodeId, Capacity = 10, ConfigVersion = 1 });
            db.NodeHealth.AddRange(
                new NodeHealth { NodeId = a.NodeId, Healthy = true, TunReady = true, TlsOk = true, QuicOk = true, CpConnected = true, UpdatedAt = DateTime.UtcNow },
                new NodeHealth { NodeId = b.NodeId, Healthy = true, TunReady = true, TlsOk = true, QuicOk = true, CpConnected = true, UpdatedAt = DateTime.UtcNow });
            await db.SaveChangesAsync();
        }
        return (sp, a, b);
    }

    private static Node MakeNode(string id, string name) => new()
    {
        NodeId = id,
        DisplayName = name,
        LocationId = "fi-helsinki",
        LifecycleState = NodeLifecycleState.Active,
        Status = NodeRuntimeStatus.Healthy,
        Enabled = true,
        LastSeenAt = DateTime.UtcNow,
        Capacity = 10,
        CurrentSessions = 0,
        ReportedServerVersion = "1.1.9",
        SupportsNodeCommands = true,
        ManagementCapabilities = "node_update,service_restart,host_reboot,certificate_renew"
    };

    [Fact]
    public async Task Start_EnqueuesOnlyFirstNode_NotParallel()
    {
        var (sp, a, b) = await SeedTwoHealthyAsync("rollout-" + Guid.NewGuid().ToString("N"));
        await using var _ = sp;

        // Sanity: direct enqueue works for SuperAdmin with sibling.
        var cmdSvc = sp.GetRequiredService<INodeCommandService>();
        var probe = await cmdSvc.EnqueueAsync(a.NodeId, NodeCommandType.UpdateNodeLatest, "sa@test",
            new[] { AdminRole.SuperAdmin });
        Assert.Equal(NodeCommandStatus.Pending, probe.Status);
        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            db.NodeCommands.RemoveRange(db.NodeCommands);
            await db.SaveChangesAsync();
            // Clear any leftover rollout key
            var keys = db.SystemSettings.Where(s => s.Key.StartsWith(LocationRolloutService.SettingKeyPrefix));
            db.SystemSettings.RemoveRange(keys);
            await db.SaveChangesAsync();
        }

        var dto = await sp.GetRequiredService<ILocationRolloutService>()
            .StartAsync("fi-helsinki", "sa@test", new[] { AdminRole.SuperAdmin });

        Assert.True(dto.Status is "Running" or "Succeeded",
            $"unexpected status={dto.Status} stop={dto.StopReason} items={string.Join(';', dto.Items.Select(i => i.Status + ":" + i.Detail))}");
        Assert.Single(dto.Items, i => i.Status == "Running" && i.CommandId is not null);
        Assert.Contains(dto.Items, i => i.Status == "Pending");

        await using var db2 = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync();
        var active = await db2.NodeCommands.CountAsync(c =>
            c.Type == NodeCommandType.UpdateNodeLatest &&
            (c.Status == NodeCommandStatus.Pending || c.Status == NodeCommandStatus.Claimed ||
             c.Status == NodeCommandStatus.Running));
        Assert.Equal(1, active);
    }

    [Fact]
    public async Task Tick_Stops_OnFailedCommand()
    {
        var (sp, a, b) = await SeedTwoHealthyAsync("rollout-fail-" + Guid.NewGuid().ToString("N"));
        await using var _ = sp;
        var rollout = sp.GetRequiredService<ILocationRolloutService>();
        var dto = await rollout.StartAsync("fi-helsinki", "sa@test", new[] { AdminRole.SuperAdmin });
        var running = dto.Items.Single(i => i.CommandId is not null);

        await using (var db = await sp.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>().CreateDbContextAsync())
        {
            var cmd = await db.NodeCommands.FirstAsync(c => c.Id == running.CommandId);
            cmd.Status = NodeCommandStatus.Failed;
            cmd.ResultCode = "rollback_failed";
            cmd.CompletedAt = DateTime.UtcNow;
            await db.SaveChangesAsync();
        }

        await rollout.TickAsync();
        var after = await rollout.GetAsync(dto.Id);
        Assert.NotNull(after);
        Assert.Equal("Stopped", after!.Status);
        Assert.Contains(after.Items, i => i.Status == "Skipped");
    }

    private sealed class RecordingAudit : IAuditService
    {
        public Task WriteAsync(Application.Contracts.V1.AuditWriteRequest request,
            CancellationToken cancellationToken = default) => Task.CompletedTask;
    }
}
