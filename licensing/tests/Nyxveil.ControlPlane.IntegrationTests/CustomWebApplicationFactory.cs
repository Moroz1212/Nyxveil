using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.TestHost;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class CustomWebApplicationFactory : WebApplicationFactory<Program>
{
    public const string TestKekHex =
        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

    private readonly string _dbName = "cp-int-" + Guid.NewGuid().ToString("N");

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.UseSetting("ConnectionStrings:ControlPlane",
            "Server=(localdb)\\mssqllocaldb;Database=NyxveilControlPlane_Unused;Trusted_Connection=True;TrustServerCertificate=True");
        builder.UseSetting("Security:LicenseKekHex", TestKekHex);
        builder.UseSetting("Https:RequireHttpsInProduction", "false");
        builder.UseSetting("Setup:AllowWebBootstrap", "true");

        builder.ConfigureTestServices(services =>
        {
            RemoveDbContextRegistrations(services);

            services.AddDbContext<ControlPlaneDbContext>(options =>
                options.UseInMemoryDatabase(_dbName)
                    .ConfigureWarnings(w => w.Ignore(
                        Microsoft.EntityFrameworkCore.Diagnostics.InMemoryEventId.TransactionIgnoredWarning)));

            foreach (var d in services.Where(IsControlPlaneWorker).ToList())
                services.Remove(d);

            services.ConfigureApplicationCookie(options =>
            {
                options.Cookie.SecurePolicy = Microsoft.AspNetCore.Http.CookieSecurePolicy.SameAsRequest;
            });
        });
    }

    protected override IHost CreateHost(IHostBuilder builder)
    {
        var host = base.CreateHost(builder);
        using var scope = host.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        db.Database.EnsureCreated();
        return host;
    }

    private static bool IsControlPlaneWorker(ServiceDescriptor d) =>
        d.ServiceType == typeof(IHostedService) &&
        d.ImplementationType?.Namespace?.StartsWith("Nyxveil.ControlPlane.Worker", StringComparison.Ordinal) == true;

    private static void RemoveDbContextRegistrations(IServiceCollection services)
    {
        foreach (var d in services.Where(d =>
                     d.ServiceType == typeof(ControlPlaneDbContext) ||
                     d.ServiceType == typeof(DbContextOptions<ControlPlaneDbContext>) ||
                     d.ImplementationType == typeof(ControlPlaneDbContext) ||
                     d.ServiceType.FullName?.Contains("ControlPlaneDbContext", StringComparison.Ordinal) == true)
                     .ToList())
        {
            services.Remove(d);
        }
    }
}
