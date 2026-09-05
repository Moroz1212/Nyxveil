using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class DashboardQueryServiceTests
{
    [Fact]
    public async Task GetSummaryAsync_UsesIsolatedFactoryContext_AndCompletes()
    {
        var dbName = "dash-" + Guid.NewGuid().ToString("N");
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddDbContextFactory<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase(dbName));
        services.AddScoped<IDashboardQueryService, DashboardQueryService>();

        await using var provider = services.BuildServiceProvider();
        await using (var seed = await provider.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>()
                         .CreateDbContextAsync())
        {
            await seed.Database.EnsureCreatedAsync();
        }

        var dash = provider.GetRequiredService<IDashboardQueryService>();
        var summary = await dash.GetSummaryAsync();
        Assert.NotNull(summary);
        Assert.Equal(0, summary.TotalNodes);
    }

    [Fact]
    public async Task ConcurrentGetSummaryAsync_DoesNotThrowConcurrencyDetector()
    {
        var dbName = "dash-parallel-" + Guid.NewGuid().ToString("N");
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IClock>(new FakeClock(DateTime.UtcNow));
        services.AddDbContextFactory<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase(dbName));
        services.AddScoped<IDashboardQueryService, DashboardQueryService>();

        await using var provider = services.BuildServiceProvider();
        await using (var seed = await provider.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>()
                         .CreateDbContextAsync())
        {
            await seed.Database.EnsureCreatedAsync();
        }

        await using var scope1 = provider.CreateAsyncScope();
        await using var scope2 = provider.CreateAsyncScope();
        var a = scope1.ServiceProvider.GetRequiredService<IDashboardQueryService>();
        var b = scope2.ServiceProvider.GetRequiredService<IDashboardQueryService>();

        var ex = await Record.ExceptionAsync(() => Task.WhenAll(a.GetSummaryAsync(), b.GetSummaryAsync()));
        Assert.Null(ex);
    }
}
