using Microsoft.Data.SqlClient;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.IntegrationTests;

[Collection(SqlServerIntegrationCollection.Name)]
public sealed class ManagementCommandConcurrencyTests
{
    [Fact]
    public Task RelationalConcurrentEnqueue_OnlyOneLocationOperation() => RunAsync(false, RaceAsync);

    [ConditionalSqlServerFact]
    public Task SqlConcurrentEnqueue_OnlyOneLocationOperation() => RunAsync(true, RaceAsync);

    [ConditionalSqlServerFact]
    public Task SqlApplicationLock_IsSharedWithIndependentConnection() => RunAsync(true, async create =>
    {
        await using var db = create();
        await using var connection = new SqlConnection(db.Database.GetConnectionString());
        await connection.OpenAsync();
        await using var tx = (SqlTransaction)await connection.BeginTransactionAsync();
        await using var command = connection.CreateCommand();
        command.Transaction = tx;
        command.CommandText = "EXEC sys.sp_getapplock @Resource='nyxveil:location:test-location', @LockMode='Exclusive', @LockOwner='Transaction', @LockTimeout=5000;";
        await command.ExecuteNonQueryAsync();
        await Assert.ThrowsAsync<ConflictException>(() => Service(db).EnqueueAsync("a", NodeCommandType.RestartNyxveilService, "test", [AdminRole.SuperAdmin]));
        Assert.Empty(await db.NodeCommands.ToListAsync());
        await tx.RollbackAsync();
        Assert.NotNull(await Service(db).EnqueueAsync("a", NodeCommandType.RestartNyxveilService, "test", [AdminRole.SuperAdmin]));
    });

    [Fact]
    public Task RelationalDuplicateClaim_OnlyOneDelivery() => RunAsync(false, async create =>
    {
        await using (var seed = create())
            await Service(seed).EnqueueAsync("a", NodeCommandType.RenewCertificate, "test", [AdminRole.SuperAdmin]);
        async Task<NodeCommand?> Claim()
        {
            await using var db = create();
            return await Service(db).ClaimNextAsync("a");
        }
        var claims = await Task.WhenAll(Claim(), Claim());
        Assert.Single(claims.Where(c => c is not null));
    });

    private static async Task RaceAsync(Func<ControlPlaneDbContext> create)
    {
        async Task<bool> Enqueue(string node)
        {
            await using var db = create();
            try { await Service(db).EnqueueAsync(node, NodeCommandType.RestartNyxveilService, "test", [AdminRole.SuperAdmin]); return true; }
            catch (ConflictException) { return false; }
        }
        var result = await Task.WhenAll(Enqueue("a"), Enqueue("b"));
        Assert.Single(result.Where(success => success));
        await using var verify = create();
        Assert.Single(await verify.NodeCommands.ToListAsync());
    }

    private static NodeCommandService Service(ControlPlaneDbContext db) => new(db, new Clock(), new Audit(), new Releases());

    private static async Task RunAsync(bool sql, Func<Func<ControlPlaneDbContext>, Task> test)
    {
        var name = sql ? await SqlServerAvailability.CreateDatabaseAsync() : Path.Combine(Path.GetTempPath(), Guid.NewGuid() + ".db");
        ControlPlaneDbContext Create() => sql
            ? new ControlPlaneDbContext(new DbContextOptionsBuilder<ControlPlaneDbContext>().UseSqlServer(SqlServerAvailability.DatabaseConnectionString(name)).Options)
            : new RelationalTestDbContext(new DbContextOptionsBuilder<RelationalTestDbContext>().UseSqlite($"Data Source={name};Pooling=False").Options);
        try
        {
            await using (var db = Create())
            {
                await db.Database.EnsureCreatedAsync();
                db.Locations.Add(new Location { LocationId = "test-location", Code = "test", DisplayName = "Test" });
                foreach (var id in new[] { "a", "b" })
                {
                    db.Nodes.Add(new Node { NodeId = id, LocationId = "test-location", DisplayName = id, PublicIdentity = new byte[32],
                        Status = NodeRuntimeStatus.Healthy, Capacity = 100, LastSeenAt = DateTime.UtcNow,
                        SupportsNodeCommands = true, ManagementCapabilities = "service_restart,certificate_renew,node_update,host_reboot" });
                    db.NodeConfigs.Add(new NodeConfig { NodeId = id, Enabled = true, Capacity = 100 });
                }
                await db.SaveChangesAsync();
            }
            await test(Create);
        }
        finally
        {
            if (sql) await SqlServerAvailability.DropDatabaseAsync(name);
            else File.Delete(name);
        }
    }

    private sealed class Clock : IClock { public DateTime UtcNow => DateTime.UtcNow; }
    private sealed class Audit : IAuditService { public Task WriteAsync(AuditWriteRequest request, CancellationToken cancellationToken = default) => Task.CompletedTask; }
    private sealed class Releases : IServerReleaseService
    {
        public Task<ServerReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default) => Task.FromResult(new ServerReleaseInfo { LatestVersion = "1.1.11", ReleaseTag = "server-v1.1.11", SourceStatus = "ok" });
        public Task<ServerReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default) => GetLatestAsync(cancellationToken);
    }
}
