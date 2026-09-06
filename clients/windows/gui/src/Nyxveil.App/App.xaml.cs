using System.Windows;
using Nyxveil.Client.Core;

namespace Nyxveil.App;

public partial class App : Application
{
    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        var settings = ClientSettings.Load();
        if (!LicenseCredentialStore.Exists())
        {
            var licenseWindow = new LicenseWindow(settings);
            var ok = licenseWindow.ShowDialog();
            if (ok != true)
            {
                Shutdown();
                return;
            }
        }

        var main = new MainWindow(settings);
        MainWindow = main;
        main.Show();
    }
}
