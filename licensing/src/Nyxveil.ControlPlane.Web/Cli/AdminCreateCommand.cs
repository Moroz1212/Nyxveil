using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Identity;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Creates the first SuperAdmin. Prefer stdin password (installer pipes SecureString).
/// Env var NYXVEIL_ADMIN_PASSWORD is accepted only for automated tests.
/// Never logs the password. Exit 2 if a SuperAdmin already exists.
/// </summary>
public static class AdminCreateCommand
{
    public const int ExitSuccess = 0;
    public const int ExitAlreadyExists = 2;
    public const int ExitError = 1;

    public static async Task<int> RunAsync(string[] args, CancellationToken cancellationToken = default)
    {
        string? username = null;
        for (var i = 0; i < args.Length; i++)
        {
            if (args[i] is "--username" or "-u")
            {
                if (i + 1 >= args.Length)
                {
                    Console.Error.WriteLine("Usage: admin create --username <email>");
                    return ExitError;
                }

                username = args[++i];
            }
        }

        if (string.IsNullOrWhiteSpace(username))
        {
            Console.Error.WriteLine("Usage: admin create --username <email>");
            return ExitError;
        }

        username = username.Trim();
        var password = ReadPassword();
        if (string.IsNullOrEmpty(password))
        {
            Console.Error.WriteLine("Password is required (stdin or NYXVEIL_ADMIN_PASSWORD for tests).");
            return ExitError;
        }

        try
        {
            var builder = Host.CreateApplicationBuilder(args);
            // CLI must not fail-closed on HTTPS certificates.
            builder.Configuration["Https:RequireHttpsInProduction"] = "false";

            var programData = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
                "Nyxveil",
                "ControlPlane");
            if (Directory.Exists(programData))
                builder.Configuration.AddNyxveilProtectedSecrets(programData);

            builder.Services.AddApplication(builder.Configuration);
            builder.Services.AddInfrastructure(builder.Configuration);

            using var host = builder.Build();
            using var scope = host.Services.CreateScope();

            var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole>>();
            var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();

            await RoleSeeder.EnsureRolesAsync(roleManager, cancellationToken).ConfigureAwait(false);

            var existing = await userManager.GetUsersInRoleAsync(AdminRole.SuperAdmin).ConfigureAwait(false);
            if (existing.Count > 0)
            {
                Console.Error.WriteLine("A SuperAdmin already exists. Refusing to create another via CLI.");
                return ExitAlreadyExists;
            }

            var user = new ApplicationUser
            {
                UserName = username,
                Email = username,
                EmailConfirmed = true,
                DisplayName = username
            };

            var create = await userManager.CreateAsync(user, password).ConfigureAwait(false);
            if (!create.Succeeded)
            {
                foreach (var err in create.Errors)
                    Console.Error.WriteLine(err.Description);
                return ExitError;
            }

            await userManager.AddToRoleAsync(user, AdminRole.SuperAdmin).ConfigureAwait(false);
            Console.WriteLine($"SuperAdmin '{username}' created.");
            return ExitSuccess;
        }
        finally
        {
            password = null;
        }
    }

    private static string? ReadPassword()
    {
        if (!Console.IsInputRedirected)
        {
            var fromEnv = Environment.GetEnvironmentVariable("NYXVEIL_ADMIN_PASSWORD");
            if (!string.IsNullOrEmpty(fromEnv))
                return fromEnv;

            Console.Error.WriteLine("Password: (waiting for stdin line; prefer installer pipe)");
        }

        var line = Console.In.ReadLine();
        if (!string.IsNullOrEmpty(line))
            return line;

        return Environment.GetEnvironmentVariable("NYXVEIL_ADMIN_PASSWORD");
    }
}
