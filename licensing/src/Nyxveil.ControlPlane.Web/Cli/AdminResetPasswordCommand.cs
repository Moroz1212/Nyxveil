using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Identity;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Resets an admin password. Local CLI only (no web endpoint). Password from stdin.
/// Usage: admin reset-password --username &lt;email&gt;
/// </summary>
public static class AdminResetPasswordCommand
{
    public static async Task<int> RunAsync(string[] args, CancellationToken cancellationToken = default)
    {
        string? username = null;
        for (var i = 0; i < args.Length; i++)
        {
            if (args[i] is "--username" or "-u")
            {
                if (i + 1 >= args.Length)
                {
                    Console.Error.WriteLine("Usage: admin reset-password --username <email>");
                    return 1;
                }

                username = args[++i];
            }
        }

        if (string.IsNullOrWhiteSpace(username))
        {
            Console.Error.WriteLine("Usage: admin reset-password --username <email>");
            return 1;
        }

        username = username.Trim();
        var password = ReadPassword();
        if (string.IsNullOrEmpty(password))
        {
            Console.Error.WriteLine("Password is required (stdin or NYXVEIL_ADMIN_PASSWORD for tests).");
            return 1;
        }

        try
        {
            var builder = Host.CreateApplicationBuilder(args);
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
            var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();

            var user = await userManager.FindByEmailAsync(username).ConfigureAwait(false)
                       ?? await userManager.FindByNameAsync(username).ConfigureAwait(false);
            if (user is null)
            {
                Console.Error.WriteLine("User not found.");
                return 1;
            }

            var token = await userManager.GeneratePasswordResetTokenAsync(user).ConfigureAwait(false);
            var result = await userManager.ResetPasswordAsync(user, token, password).ConfigureAwait(false);
            if (!result.Succeeded)
            {
                foreach (var err in result.Errors)
                    Console.Error.WriteLine(err.Description);
                return 1;
            }

            Console.WriteLine($"Password reset for '{username}'.");
            return 0;
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
