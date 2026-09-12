using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.TestHost;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.BrowserE2E;

internal sealed class BrowserWebApplicationFactory : WebApplicationFactory<Program>
{
    private const string TestKekHex =
        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    private readonly string _dbPath =
        Path.Combine(Path.GetTempPath(), "cp-browser-" + Guid.NewGuid().ToString("N") + ".db");

    public const string AdminEmail = "browser-admin@example.test";
    public const string AdminPassword = "BrowserAdmin!23456";
    public const string NodeId = "browser-node-1";

    public string TotpSecret { get; private set; } = "";

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.UseSetting("ConnectionStrings:ControlPlane",
            "Server=(localdb)\\mssqllocaldb;Database=NyxveilControlPlane_Unused;Trusted_Connection=True");
        builder.UseSetting("Security:LicenseKekHex", TestKekHex);
        builder.UseSetting("Https:RequireHttpsInProduction", "false");
        builder.UseSetting("Setup:AllowWebBootstrap", "false");

        builder.ConfigureTestServices(services =>
        {
            RemoveDbContextRegistrations(services);

            using (var schema = CreateContext())
                schema.Database.EnsureCreated();

            services.AddDbContext<BrowserTestDbContext>(options =>
                options.UseSqlite($"Data Source={_dbPath};Pooling=False"));
            services.AddScoped<ControlPlaneDbContext>(sp =>
                sp.GetRequiredService<BrowserTestDbContext>());
            services.AddSingleton<IDbContextFactory<ControlPlaneDbContext>>(
                new BrowserDbContextFactory(_dbPath));

            foreach (var descriptor in services.Where(IsControlPlaneWorker).ToList())
                services.Remove(descriptor);

            services.ConfigureApplicationCookie(options =>
            {
                options.Cookie.SecurePolicy =
                    Microsoft.AspNetCore.Http.CookieSecurePolicy.SameAsRequest;
            });

            services.RemoveAll<IServerReleaseService>();
            services.AddSingleton<IServerReleaseService, BrowserServerReleaseService>();
            services.RemoveAll<IControlPlaneReleaseService>();
            services.AddSingleton<IControlPlaneReleaseService, BrowserControlPlaneReleaseService>();
        });
    }

    public async Task SeedAsync()
    {
        await using var scope = Services.CreateAsyncScope();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole>>();
        foreach (var role in new[] { AdminRole.SuperAdmin, AdminRole.Operator, AdminRole.ReadOnly })
            if (!await roles.RoleExistsAsync(role))
                AssertSucceeded(await roles.CreateAsync(new IdentityRole(role)));

        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser
        {
            UserName = AdminEmail,
            Email = AdminEmail,
            EmailConfirmed = true,
            DisplayName = "Browser E2E"
        };
        AssertSucceeded(await users.CreateAsync(user, AdminPassword));
        AssertSucceeded(await users.AddToRoleAsync(user, AdminRole.SuperAdmin));
        AssertSucceeded(await users.ResetAuthenticatorKeyAsync(user));
        TotpSecret = await users.GetAuthenticatorKeyAsync(user)
            ?? throw new InvalidOperationException("Identity did not issue an authenticator key.");
        AssertSucceeded(await users.SetTwoFactorEnabledAsync(user, true));

        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var now = DateTime.UtcNow;
        db.Locations.Add(new Location
        {
            LocationId = "browser-location",
            Code = "brw",
            Country = "Test",
            City = "Browser",
            DisplayName = "Browser",
            Enabled = true,
            SortOrder = 1,
            CreatedAt = now,
            UpdatedAt = now
        });
        db.Nodes.Add(new Node
        {
            NodeId = NodeId,
            LocationId = "browser-location",
            DisplayName = "Browser Node",
            LifecycleState = NodeLifecycleState.Active,
            Status = NodeRuntimeStatus.Healthy,
            Enabled = true,
            Capacity = 10,
            LastSeenAt = now,
            CreatedAt = now,
            UpdatedAt = now,
            ReportedServerVersion = "1.0.0",
            VersionReportedAt = now,
            SupportsNodeCommands = true,
            ManagementCapabilities = "node_update,certificate_renew,service_restart,host_reboot"
        });
        db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = NodeId,
            Enabled = true,
            Capacity = 10,
            ConfigVersion = 1,
            UpdatedAt = now
        });
        await db.SaveChangesAsync();
    }

    public async Task<bool> HasCertificateRenewCommandAsync()
    {
        await using var scope = Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        return await db.NodeCommands.AnyAsync(c =>
            c.NodeId == NodeId && c.Type == NodeCommandType.RenewCertificate);
    }

    protected override void Dispose(bool disposing)
    {
        base.Dispose(disposing);
        if (disposing)
            try { File.Delete(_dbPath); } catch (IOException) { }
    }

    private BrowserTestDbContext CreateContext() =>
        new(new DbContextOptionsBuilder<BrowserTestDbContext>()
            .UseSqlite($"Data Source={_dbPath};Pooling=False").Options);

    private static void AssertSucceeded(IdentityResult result)
    {
        if (!result.Succeeded)
            throw new InvalidOperationException(string.Join("; ", result.Errors.Select(e => e.Description)));
    }

    private static bool IsControlPlaneWorker(ServiceDescriptor descriptor) =>
        descriptor.ServiceType == typeof(IHostedService) &&
        descriptor.ImplementationType?.Namespace?.StartsWith(
            "Nyxveil.ControlPlane.Worker", StringComparison.Ordinal) == true;

    private static void RemoveDbContextRegistrations(IServiceCollection services)
    {
        foreach (var descriptor in services.Where(d =>
                     d.ServiceType == typeof(ControlPlaneDbContext) ||
                     d.ServiceType == typeof(DbContextOptions<ControlPlaneDbContext>) ||
                     d.ServiceType == typeof(IDbContextFactory<ControlPlaneDbContext>) ||
                     d.ImplementationType == typeof(ControlPlaneDbContext) ||
                     d.ServiceType.FullName?.Contains(
                         "ControlPlaneDbContext", StringComparison.Ordinal) == true).ToList())
            services.Remove(descriptor);
    }

    private sealed class BrowserDbContextFactory(string path)
        : IDbContextFactory<ControlPlaneDbContext>
    {
        public ControlPlaneDbContext CreateDbContext() =>
            new BrowserTestDbContext(new DbContextOptionsBuilder<BrowserTestDbContext>()
                .UseSqlite($"Data Source={path};Pooling=False").Options);

        public ValueTask<ControlPlaneDbContext> CreateDbContextAsync(
            CancellationToken cancellationToken = default) => new(CreateDbContext());
    }

    private sealed class BrowserServerReleaseService : IServerReleaseService
    {
        private static readonly ServerReleaseInfo Release = new()
        {
            LatestVersion = "9.9.9",
            ReleaseTag = "server-v9.9.9",
            SourceStatus = "cached",
            LastCheckedAt = DateTimeOffset.UtcNow
        };

        public Task<ServerReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(Release);

        public Task<ServerReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(Release);
    }

    private sealed class BrowserControlPlaneReleaseService : IControlPlaneReleaseService
    {
        private static readonly ControlPlaneReleaseInfo Release = new()
        {
            LatestVersion = "9.9.9",
            ReleaseTag = "control-plane-v9.9.9",
            PackageName = "candidate.zip",
            PackageUrl = "https://example.test/candidate.zip",
            ChecksumName = "candidate.zip.sha256",
            ChecksumUrl = "https://example.test/candidate.zip.sha256",
            ExpectedSha256 = new string('a', 64),
            SourceStatus = "cached",
            LastCheckedAt = DateTimeOffset.UtcNow
        };

        public Task<ControlPlaneReleaseInfo> GetLatestAsync(
            CancellationToken cancellationToken = default) => Task.FromResult(Release);

        public Task<ControlPlaneReleaseInfo> RefreshAsync(
            CancellationToken cancellationToken = default) => Task.FromResult(Release);
    }
}

internal sealed class BrowserTestDbContext(DbContextOptions<BrowserTestDbContext> options)
    : ControlPlaneDbContext(options)
{
    protected override void OnModelCreating(ModelBuilder builder)
    {
        base.OnModelCreating(builder);
        foreach (var entity in builder.Model.GetEntityTypes())
            foreach (var check in entity.GetCheckConstraints().ToArray())
                entity.RemoveCheckConstraint(check.Name!);
    }
}
