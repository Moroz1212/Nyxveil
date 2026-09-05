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

    private readonly string _dbName = Path.Combine(Path.GetTempPath(), "cp-int-" + Guid.NewGuid().ToString("N") + ".db");

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

            using (var schema = new RelationalTestDbContext(new DbContextOptionsBuilder<RelationalTestDbContext>()
                .UseSqlite($"Data Source={_dbName};Pooling=False").Options)) schema.Database.EnsureCreated();

            services.AddDbContext<RelationalTestDbContext>(options =>
                options.UseSqlite($"Data Source={_dbName};Pooling=False"));
            services.AddScoped<ControlPlaneDbContext>(sp => sp.GetRequiredService<RelationalTestDbContext>());
            services.AddSingleton<IDbContextFactory<ControlPlaneDbContext>>(
                new TestControlPlaneDbContextFactory(_dbName));

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
                     d.ServiceType == typeof(IDbContextFactory<ControlPlaneDbContext>) ||
                     d.ImplementationType == typeof(ControlPlaneDbContext) ||
                     d.ServiceType.FullName?.Contains("ControlPlaneDbContext", StringComparison.Ordinal) == true)
                     .ToList())
        {
            services.Remove(d);
        }
    }

    private sealed class TestControlPlaneDbContextFactory : IDbContextFactory<ControlPlaneDbContext>
    {
        private readonly string _dbName;

        public TestControlPlaneDbContextFactory(string dbName) => _dbName = dbName;

        public ControlPlaneDbContext CreateDbContext()
        {
            var options = new DbContextOptionsBuilder<RelationalTestDbContext>()
                .UseSqlite($"Data Source={_dbName};Pooling=False")
                .Options;
            return new RelationalTestDbContext(options);
        }

        public ValueTask<ControlPlaneDbContext> CreateDbContextAsync(CancellationToken cancellationToken = default) =>
            new(CreateDbContext());
    }
}

// SQL Server DDL is verified separately. SQLite exercises relational transactions,
// uniqueness and optimistic concurrency; only SQL Server-specific CHECK syntax is omitted.
public sealed class RelationalTestDbContext : ControlPlaneDbContext
{
    public RelationalTestDbContext(DbContextOptions<RelationalTestDbContext> options) : base(options) { }
    protected override void OnModelCreating(ModelBuilder builder)
    {
        base.OnModelCreating(builder);
        foreach (var entity in builder.Model.GetEntityTypes())
            foreach (var check in entity.GetCheckConstraints().ToArray())
                entity.RemoveCheckConstraint(check.Name!);
    }
}
