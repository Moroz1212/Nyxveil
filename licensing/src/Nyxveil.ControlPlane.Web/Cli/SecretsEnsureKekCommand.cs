using Nyxveil.ControlPlane.Infrastructure.Security;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>
/// Ensures License KEK DPAPI file exists (creates only if absent).
/// Usage: secrets ensure-kek
/// </summary>
public static class SecretsEnsureKekCommand
{
    public static int Run()
    {
        try
        {
            if (!OperatingSystem.IsWindows())
            {
                Console.Error.WriteLine("secrets ensure-kek requires Windows DPAPI.");
                return 1;
            }

            var created = LicenseKekBackupService.EnsureKekExists();
            Console.WriteLine(created
                ? "Created license-kek.dpapi (new KEK)."
                : "license-kek.dpapi already present; left unchanged.");
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine(ex.Message);
            return 1;
        }
    }
}
