using System.Windows;
using Nyxveil.Client.Core;

namespace Nyxveil.App;

public partial class App : Application
{
    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        string? visualQa = null;
        string? screenshotPath = null;
        foreach (var arg in e.Args)
        {
            if (arg.StartsWith("--visual-qa=", StringComparison.OrdinalIgnoreCase))
                visualQa = arg["--visual-qa=".Length..];
            else if (arg.StartsWith("--screenshot=", StringComparison.OrdinalIgnoreCase))
                screenshotPath = arg["--screenshot=".Length..];
        }

        var settings = ClientSettings.Load();
        if (visualQa is null && !LicenseCredentialStore.Exists())
        {
            var licenseWindow = new LicenseWindow(settings);
            var ok = licenseWindow.ShowDialog();
            if (ok != true)
            {
                Shutdown();
                return;
            }
        }

        var main = new MainWindow(settings, visualQa, screenshotPath);
        MainWindow = main;
        main.Show();
    }
}
