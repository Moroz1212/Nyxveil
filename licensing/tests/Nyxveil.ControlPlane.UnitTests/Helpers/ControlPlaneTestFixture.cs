using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.UnitTests.Helpers;

/// <summary>
/// Builds an InMemory ControlPlaneDbContext with known KEK and ephemeral Ed25519 signing keys.
/// </summary>
public sealed class ControlPlaneTestFixture : IAsyncDisposable
{
    public const string TestKekHex =
        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

    private readonly ServiceProvider _provider;

    public ControlPlaneTestFixture()
    {
        var dbName = "cp-unit-" + Guid.NewGuid().ToString("N");
        Clock = new FakeClock(DateTime.UtcNow);

        var services = new ServiceCollection();
        services.AddLogging();
        services.AddDataProtection();
        services.AddSingleton<IClock>(Clock);
        services.AddSingleton(Options.Create(new SecurityOptions { LicenseKekHex = TestKekHex }));
        services.AddSingleton(Options.Create(new SigningOptions
        {
            Issuer = "nyxveil-control-plane-test",
            Audience = "nvp-node"
        }));
        services.AddSingleton(Options.Create(new TicketOptions { TtlMinutes = 15 }));
        services.AddSingleton(Options.Create(new NodeAuthOptions { AllowLegacyBearer = true }));

        services.AddDbContext<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase(dbName)
                .ConfigureWarnings(w => w.Ignore(
                    Microsoft.EntityFrameworkCore.Diagnostics.InMemoryEventId.TransactionIgnoredWarning)));

        services.AddSingleton<ILicenseKeyHasher, LicenseKeyHasher>();
        services.AddSingleton<ISigningKeyService, Ed25519SigningKeyStore>();
        services.AddSingleton<ICatalogSigner, CatalogSigner>();
        services.AddScoped<AccessTicketService>();
        services.AddScoped<IAccessTicketIssuer>(sp => sp.GetRequiredService<AccessTicketService>());
        services.AddScoped<NodeAuthService>();
        services.AddScoped<INodeAuthenticator>(sp => sp.GetRequiredService<NodeAuthService>());
        services.AddScoped<ILicenseProvisioningService, LicenseProvisioningService>();
        services.AddScoped<IDeviceService, DeviceService>();
        services.AddScoped<INodeRegistrationService, NodeRegistrationService>();
        services.AddScoped<INodeManagementService, NodeManagementService>();
        services.AddScoped<INodeHeartbeatService, NodeHeartbeatService>();
        services.AddScoped<ICatalogService, CatalogService>();
        services.AddScoped<ITicketService, TicketService>();
        services.AddScoped<IBootstrapTokenService, BootstrapTokenService>();
        services.AddScoped<IAuditService, AuditService>();

        _provider = services.BuildServiceProvider();
        Scope = _provider.CreateScope();
        Db = Scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        Hasher = Scope.ServiceProvider.GetRequiredService<ILicenseKeyHasher>();
        Licenses = Scope.ServiceProvider.GetRequiredService<ILicenseProvisioningService>();
        Devices = Scope.ServiceProvider.GetRequiredService<IDeviceService>();
        Tickets = Scope.ServiceProvider.GetRequiredService<ITicketService>();
        TicketIssuer = Scope.ServiceProvider.GetRequiredService<AccessTicketService>();
        Bootstrap = Scope.ServiceProvider.GetRequiredService<IBootstrapTokenService>();
        Nodes = Scope.ServiceProvider.GetRequiredService<INodeRegistrationService>();
        NodeManagement = Scope.ServiceProvider.GetRequiredService<INodeManagementService>();
        Heartbeats = Scope.ServiceProvider.GetRequiredService<INodeHeartbeatService>();
        Catalog = Scope.ServiceProvider.GetRequiredService<ICatalogService>();
        NodeAuth = Scope.ServiceProvider.GetRequiredService<NodeAuthService>();

        Db.Database.EnsureCreated();
        SeedBaselineAsync().GetAwaiter().GetResult();
    }

    public FakeClock Clock { get; }
    public IServiceScope Scope { get; }
    public ControlPlaneDbContext Db { get; }
    public ILicenseKeyHasher Hasher { get; }
    public ILicenseProvisioningService Licenses { get; }
    public IDeviceService Devices { get; }
    public ITicketService Tickets { get; }
    public AccessTicketService TicketIssuer { get; }
    public IBootstrapTokenService Bootstrap { get; }
    public INodeRegistrationService Nodes { get; }
    public INodeManagementService NodeManagement { get; }
    public INodeHeartbeatService Heartbeats { get; }
    public ICatalogService Catalog { get; }
    public NodeAuthService NodeAuth { get; }

    public Guid StandardPlanId { get; private set; }
    public Guid MasterPlanId { get; private set; }
    public string LocationId { get; private set; } = "loc-ams";
    public string LocationCode { get; private set; } = "ams";
    public string LocationCodeB { get; private set; } = "fra";
    public string LocationIdB { get; private set; } = "loc-fra";

    private async Task SeedBaselineAsync()
    {
        var now = Clock.UtcNow;
        StandardPlanId = Guid.NewGuid();
        MasterPlanId = Guid.NewGuid();

        Db.Plans.AddRange(
            new Plan
            {
                PlanId = StandardPlanId,
                Code = "standard",
                Name = "Standard",
                Status = "Active",
                DurationDays = 30,
                MaxDevices = 2,
                AllowedLocationsPolicy = "[]",
                Permissions = """["connect"]""",
                CreatedAt = now,
                UpdatedAt = now
            },
            new Plan
            {
                PlanId = MasterPlanId,
                Code = "master",
                Name = "Master",
                Status = "Active",
                DurationDays = 365,
                MaxDevices = 10,
                AllowedLocationsPolicy = "*",
                Permissions = """["connect"]""",
                CreatedAt = now,
                UpdatedAt = now
            });

        Db.Locations.AddRange(
            new Location
            {
                LocationId = LocationId,
                Code = LocationCode,
                Country = "Netherlands",
                City = "Amsterdam",
                DisplayName = "Amsterdam",
                CountryCode = "NL",
                Enabled = true,
                SortOrder = 1,
                CreatedAt = now,
                UpdatedAt = now
            },
            new Location
            {
                LocationId = LocationIdB,
                Code = LocationCodeB,
                Country = "Germany",
                City = "Frankfurt",
                DisplayName = "Frankfurt",
                CountryCode = "DE",
                Enabled = true,
                SortOrder = 2,
                CreatedAt = now,
                UpdatedAt = now
            });

        await Db.SaveChangesAsync();
    }

    public static byte[] RandomKey32()
    {
        var bytes = new byte[32];
        Random.Shared.NextBytes(bytes);
        return bytes;
    }

    public async ValueTask DisposeAsync()
    {
        Scope.Dispose();
        await _provider.DisposeAsync();
    }
}
