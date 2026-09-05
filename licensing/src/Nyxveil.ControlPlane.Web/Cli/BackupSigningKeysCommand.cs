using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Portable signing-key backup (decrypt DPAPI → PBKDF2+AES-GCM JSON, optionally ZIP-wrapped).
/// Usage:
///   backup-signing-keys export --output path.json|.zip
///   backup-signing-keys import --input path.json|.zip [--force]
/// Password from stdin (preferred) or NYXVEIL_BACKUP_PASSWORD (tests only).
/// </summary>
public static class BackupSigningKeysCommand
{
    public static async Task<int> RunAsync(string[] args, CancellationToken cancellationToken = default)
    {
        if (args.Length < 2)
        {
            Console.Error.WriteLine("Usage: backup-signing-keys export|import --output|--input <path> [--force]");
            return 1;
        }

        var action = args[1].ToLowerInvariant();
        string? path = null;
        var force = false;
        for (var i = 2; i < args.Length; i++)
        {
            if ((args[i] is "--output" or "--input" or "-o" or "-i") && i + 1 < args.Length)
                path = args[++i];
            if (args[i] is "--force" or "-f")
                force = true;
        }

        if (string.IsNullOrWhiteSpace(path))
        {
            Console.Error.WriteLine("Path is required (--output / --input).");
            return 1;
        }

        var password = Console.In.ReadLine()
                       ?? Environment.GetEnvironmentVariable("NYXVEIL_BACKUP_PASSWORD");
        if (string.IsNullOrEmpty(password))
        {
            Console.Error.WriteLine("Password required on stdin.");
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
            var backup = host.Services.GetRequiredService<ISigningKeyBackupService>();

            if (action == "export")
            {
                await using var fs = File.Create(path);
                if (path.EndsWith(".zip", StringComparison.OrdinalIgnoreCase))
                    await backup.ExportEncryptedZipAsync(fs, password, cancellationToken).ConfigureAwait(false);
                else
                    await backup.ExportPortableAsync(fs, password, cancellationToken).ConfigureAwait(false);
                Console.WriteLine("Exported portable signing-key backup.");
                return 0;
            }

            if (action == "import")
            {
                await using var fs = File.OpenRead(path);
                if (path.EndsWith(".zip", StringComparison.OrdinalIgnoreCase))
                    await backup.ImportEncryptedZipAsync(fs, password, force, cancellationToken).ConfigureAwait(false);
                else
                    await backup.ImportPortableAsync(fs, password, force, cancellationToken).ConfigureAwait(false);
                Console.WriteLine("Imported portable signing-key backup.");
                return 0;
            }

            Console.Error.WriteLine("Unknown action. Use export or import.");
            return 1;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }
}
