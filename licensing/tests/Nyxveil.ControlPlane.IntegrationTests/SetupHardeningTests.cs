using System.Net;
using System.Net.Http.Headers;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.TestHost;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class SetupHardeningTests
{
    [Fact]
    public async Task TestSetupUnavailableAfterAdminCreated()
    {
        await using var factory = new SetupTestFactory(allowWebBootstrap: true);
        var client = factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true
        });

        var email = $"admin-{Guid.NewGuid():N}@example.com";
        const string password = "TestAdmin!23456";

        using (var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["displayName"] = "First"
        }))
        {
            var first = await client.PostAsync("/account/setup", setup);
            Assert.True((int)first.StatusCode is >= 200 and < 400);
        }

        var secondEmail = $"second-{Guid.NewGuid():N}@example.com";
        using (var setup2 = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = secondEmail,
            ["password"] = password,
            ["displayName"] = "Second"
        }))
        {
            var second = await client.PostAsync("/account/setup", setup2);
            // Guard returns 404; StatusCodePages re-execute may surface as 400 in TestServer.
            Assert.True(
                second.StatusCode is HttpStatusCode.NotFound
                    or HttpStatusCode.Forbidden
                    or HttpStatusCode.BadRequest,
                $"expected rejection, got {(int)second.StatusCode}");
            Assert.False(
                second.Headers.Location?.ToString().Contains("setup=1", StringComparison.Ordinal) == true);
        }

        using var scope = factory.Services.CreateScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var admins = await users.GetUsersInRoleAsync(AdminRole.SuperAdmin);
        Assert.Single(admins);
        Assert.Null(await users.FindByEmailAsync(secondEmail));
    }

    [Fact]
    public async Task TestSetupDisabledInProduction()
    {
        await using var factory = new SetupTestFactory(allowWebBootstrap: false, environment: Environments.Production);
        var client = factory.CreateClient(new WebApplicationFactoryClientOptions { AllowAutoRedirect = false });

        // POST bootstrap must be rejected when AllowWebBootstrap=false (never auto-enabled).
        var email = $"evil-{Guid.NewGuid():N}@example.com";
        using var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = "EvilAdmin!23456",
            ["displayName"] = "Evil"
        });

        var response = await client.PostAsync("/account/setup", setup);
        Assert.True(
            response.StatusCode is HttpStatusCode.NotFound
                or HttpStatusCode.Forbidden
                or HttpStatusCode.BadRequest,
            $"expected rejection, got {(int)response.StatusCode}");

        using var scope = factory.Services.CreateScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        Assert.Empty(await users.GetUsersInRoleAsync(AdminRole.SuperAdmin));
        Assert.Null(await users.FindByEmailAsync(email));

        var configuration = scope.ServiceProvider.GetRequiredService<Microsoft.Extensions.Configuration.IConfiguration>();
        Assert.False(configuration.GetValue<bool>("Setup:AllowWebBootstrap"));
    }

    [Fact]
    public async Task TestAnonymousCannotCreateSuperAdmin()
    {
        await using var factory = new SetupTestFactory(allowWebBootstrap: false, environment: Environments.Production);
        var client = factory.CreateClient(new WebApplicationFactoryClientOptions { AllowAutoRedirect = false });

        var email = $"evil-{Guid.NewGuid():N}@example.com";
        using var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = "EvilAdmin!23456",
            ["displayName"] = "Evil"
        });

        var response = await client.PostAsync("/account/setup", setup);
        Assert.True(
            response.StatusCode is HttpStatusCode.NotFound
                or HttpStatusCode.Forbidden
                or HttpStatusCode.BadRequest,
            $"expected rejection, got {(int)response.StatusCode}");

        using var scope = factory.Services.CreateScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var admins = await users.GetUsersInRoleAsync(AdminRole.SuperAdmin);
        Assert.Empty(admins);
        Assert.Null(await users.FindByEmailAsync(email));
    }

    [Fact]
    public async Task TestFirstAdminBootstrapOneTime()
    {
        const string token = "unit-test-bootstrap-token-32chars-min!!";
        await using var factory = new SetupTestFactory(
            allowWebBootstrap: true,
            environment: Environments.Development,
            bootstrapToken: token);

        var client = factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true
        });

        var email = $"boot-{Guid.NewGuid():N}@example.com";
        const string password = "Bootstrap!23456";

        using (var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["displayName"] = "Boot",
            ["bootstrapToken"] = token
        }))
        {
            var response = await client.PostAsync("/account/setup", setup);
            Assert.True((int)response.StatusCode is >= 200 and < 400);
        }

        using var scope = factory.Services.CreateScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var admins = await users.GetUsersInRoleAsync(AdminRole.SuperAdmin);
        Assert.Single(admins);
        Assert.Equal(email, admins[0].Email);
    }

    private sealed class SetupTestFactory : WebApplicationFactory<Program>
    {
        private readonly string _dbName = "cp-setup-" + Guid.NewGuid().ToString("N");
        private readonly bool _allowWebBootstrap;
        private readonly string _environment;
        private readonly string? _bootstrapToken;

        public SetupTestFactory(
            bool allowWebBootstrap,
            string? environment = null,
            string? bootstrapToken = null)
        {
            _allowWebBootstrap = allowWebBootstrap;
            _environment = environment ?? Environments.Development;
            _bootstrapToken = bootstrapToken;
        }

        protected override void ConfigureWebHost(IWebHostBuilder builder)
        {
            builder.UseEnvironment(_environment);
            builder.UseSetting("ConnectionStrings:ControlPlane",
                "Server=(localdb)\\mssqllocaldb;Database=NyxveilControlPlane_Unused;Trusted_Connection=True;TrustServerCertificate=True");
            builder.UseSetting("Security:LicenseKekHex", CustomWebApplicationFactory.TestKekHex);
            builder.UseSetting("Https:RequireHttpsInProduction", "false");
            builder.UseSetting("Setup:AllowWebBootstrap", _allowWebBootstrap ? "true" : "false");
            if (!string.IsNullOrEmpty(_bootstrapToken))
                builder.UseSetting("Setup:BootstrapToken", _bootstrapToken);

            // Avoid Production Kestrel cert fail-closed during factory host start.
            builder.UseSetting("Certificate:Mode", "SelfSigned");
            builder.UseSetting("Hosting:BindAddress", "127.0.0.1");
            builder.UseSetting("Hosting:Port", "0");

            builder.ConfigureTestServices(services =>
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

                services.AddDbContext<ControlPlaneDbContext>(options =>
                    options.UseInMemoryDatabase(_dbName)
                        .ConfigureWarnings(w => w.Ignore(
                            Microsoft.EntityFrameworkCore.Diagnostics.InMemoryEventId.TransactionIgnoredWarning)));

                foreach (var d in services.Where(d =>
                             d.ServiceType == typeof(IHostedService) &&
                             d.ImplementationType?.Namespace?.StartsWith("Nyxveil.ControlPlane.Worker", StringComparison.Ordinal) == true)
                             .ToList())
                {
                    services.Remove(d);
                }

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
    }
}
