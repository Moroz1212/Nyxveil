using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.Infrastructure.DependencyInjection;

public static class ServiceCollectionExtensions
{
    /// <summary>
    /// ProgramData root used for secrets, keys, logs, and ASP.NET Data Protection key ring.
    /// Installer should ACL <c>data-protection</c> for the service account (Modify).
    /// </summary>
    public static string GetProgramDataRoot() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
            "Nyxveil",
            "ControlPlane");

    /// <summary>Persistent Data Protection key directory under ProgramData.</summary>
    public static string GetDataProtectionKeysDirectory() =>
        Path.Combine(GetProgramDataRoot(), "data-protection");

    public static bool ShouldPersistDataProtectionKeys(IConfiguration configuration)
    {
        if (!OperatingSystem.IsWindows())
            return false;

        var programData = GetProgramDataRoot();
        if (Directory.Exists(programData))
            return true;

        var env = configuration["ASPNETCORE_ENVIRONMENT"]
                  ?? configuration["DOTNET_ENVIRONMENT"]
                  ?? Environment.GetEnvironmentVariable("ASPNETCORE_ENVIRONMENT")
                  ?? Environment.GetEnvironmentVariable("DOTNET_ENVIRONMENT")
                  ?? Environments.Production;

        return Environments.Production.Equals(env, StringComparison.OrdinalIgnoreCase);
    }

    public static IServiceCollection AddInfrastructure(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddSingleton<IDatabaseConnectionStringProvider, DatabaseConnectionStringProvider>();

        services.AddDbContext<ControlPlaneDbContext>((sp, options) =>
        {
            var cs = sp.GetRequiredService<IDatabaseConnectionStringProvider>().GetConnectionString();
            options.UseSqlServer(cs);
        });

        services
            .AddIdentityCore<ApplicationUser>(options =>
            {
                options.User.RequireUniqueEmail = true;
                options.Password.RequiredLength = 12;
                options.Password.RequireDigit = true;
                options.Password.RequireLowercase = true;
                options.Password.RequireUppercase = true;
                options.Password.RequireNonAlphanumeric = true;
            })
            .AddRoles<IdentityRole>()
            .AddEntityFrameworkStores<ControlPlaneDbContext>()
            .AddSignInManager()
            .AddDefaultTokenProviders();

        ConfigureDataProtection(services, configuration);

        services.AddSingleton<ILicenseKeyHasher, LicenseKeyHasher>();
        services.AddSingleton<Ed25519SigningKeyStore>();
        services.AddSingleton<ISigningKeyService>(sp => sp.GetRequiredService<Ed25519SigningKeyStore>());
        services.AddSingleton<ISigningKeyBackupService, SigningKeyBackupService>();
        services.AddSingleton<ILicenseKekBackupService, LicenseKekBackupService>();
        services.AddSingleton<ISecretRecoveryService, ControlPlaneRecoveryService>();
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
        services.AddScoped<IRevocationService, RevocationService>();
        services.AddScoped<IBootstrapTokenService, BootstrapTokenService>();
        services.AddScoped<IAuditService, AuditService>();
        services.AddScoped<IDashboardQueryService, DashboardQueryService>();

        return services;
    }

    private static void ConfigureDataProtection(IServiceCollection services, IConfiguration configuration)
    {
        if (!ShouldPersistDataProtectionKeys(configuration))
        {
            services.AddDataProtection();
            return;
        }

        var keysDir = GetDataProtectionKeysDirectory();
        Directory.CreateDirectory(keysDir);

        // Installer must ACL this directory for NT SERVICE\NyxveilControlPlane (Modify).
        var builder = services.AddDataProtection()
            .PersistKeysToFileSystem(new DirectoryInfo(keysDir));

        if (OperatingSystem.IsWindows())
            builder.ProtectKeysWithDpapi(protectToLocalMachine: true);
    }
}
