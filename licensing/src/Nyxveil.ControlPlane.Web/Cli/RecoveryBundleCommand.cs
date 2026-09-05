using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Portable recovery bundle CLI (signing keys + license KEK; never SQL password).
/// Password from stdin.
/// </summary>
public static class RecoveryBundleCommand
{
    public static async Task<int> RunBackupAsync(string[] args, CancellationToken cancellationToken = default)
    {
        string? path = null;
        for (var i = 1; i < args.Length; i++)
        {
            if ((args[i] is "--output" or "-o") && i + 1 < args.Length)
                path = args[++i];
        }

        if (string.IsNullOrWhiteSpace(path))
        {
            Console.Error.WriteLine("Usage: backup-recovery --output <path.json>");
            return 1;
        }

        var password = ReadPassword();
        if (string.IsNullOrEmpty(password))
        {
            Console.Error.WriteLine("Password required on stdin.");
            return 1;
        }

        try
        {
            using var host = BuildHost(args);
            var recovery = host.Services.GetRequiredService<ISecretRecoveryService>();
            await using var fs = File.Create(path);
            await recovery.ExportRecoveryBundleAsync(fs, password, cancellationToken).ConfigureAwait(false);
            Console.WriteLine("Exported portable recovery bundle.");
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }

    public static async Task<int> RunRestoreAsync(string[] args, CancellationToken cancellationToken = default)
    {
        string? path = null;
        var force = false;
        for (var i = 1; i < args.Length; i++)
        {
            if ((args[i] is "--input" or "-i") && i + 1 < args.Length)
                path = args[++i];
            if (args[i] is "--force" or "-f")
                force = true;
        }

        if (string.IsNullOrWhiteSpace(path))
        {
            Console.Error.WriteLine("Usage: restore-recovery --input <path.json> [--force]");
            return 1;
        }

        var password = ReadPassword();
        if (string.IsNullOrEmpty(password))
        {
            Console.Error.WriteLine("Password required on stdin.");
            return 1;
        }

        try
        {
            using var host = BuildHost(args);
            var recovery = host.Services.GetRequiredService<ISecretRecoveryService>();
            await using var fs = File.OpenRead(path);
            await recovery.ImportRecoveryBundleAsync(fs, password, force, cancellationToken).ConfigureAwait(false);
            Console.WriteLine("Imported portable recovery bundle.");
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }

    private static IHost BuildHost(string[] args)
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
        return builder.Build();
    }

    private static string? ReadPassword() =>
        Console.In.ReadLine() ?? Environment.GetEnvironmentVariable("NYXVEIL_BACKUP_PASSWORD");
}
