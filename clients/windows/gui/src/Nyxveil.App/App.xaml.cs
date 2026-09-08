using System.Windows;
using Nyxveil.App.ViewModels;
using Nyxveil.Client.Core;

namespace Nyxveil.App;

public partial class App : Application
{
    private SingleInstanceGuard? _singleInstance;
    private TrayIconService? _tray;
    private MainWindow? _main;
    private ClientSettings _settings = new();
    private bool _exitRequested;
    private bool _startupLaunch;

    protected override void OnStartup(StartupEventArgs e)
    {
        // Must be set before any window closes (first-run LicenseWindow was killing the process).
        ShutdownMode = ShutdownMode.OnExplicitShutdown;
        base.OnStartup(e);

        var args = e.Args ?? Array.Empty<string>();
        if (args.Any(a => a.Equals("--quit", StringComparison.OrdinalIgnoreCase)))
        {
            // Do not call TryEnter — that would also pulse Activate on the live instance.
            SingleInstanceGuard.SignalQuit();
            Shutdown();
            return;
        }

        if (!SingleInstanceGuard.TryEnter(out _singleInstance) || _singleInstance is null)
        {
            Shutdown();
            return;
        }

        _singleInstance.StartWatching(
            () => Dispatcher.BeginInvoke(RestoreMainWindow),
            () => Dispatcher.BeginInvoke(async () => await RequestExitAsync()));

        SessionEnding += App_OnSessionEnding;

        string? visualQa = null;
        string? screenshotPath = null;
        foreach (var arg in args)
        {
            if (arg.StartsWith("--visual-qa=", StringComparison.OrdinalIgnoreCase))
                visualQa = arg["--visual-qa=".Length..];
            else if (arg.StartsWith("--screenshot=", StringComparison.OrdinalIgnoreCase))
                screenshotPath = arg["--screenshot=".Length..];
            else if (arg.Equals("--startup", StringComparison.OrdinalIgnoreCase))
                _startupLaunch = true;
        }

        _settings = ClientSettings.Load();
        // Refresh Run key (adds --startup) when Autostart already enabled from prior versions.
        if (_settings.Autostart)
            AutostartHelper.Apply(true);
        if (visualQa is null && !LicenseCredentialStore.Exists())
        {
            var licenseWindow = new LicenseWindow(_settings);
            BrandIcon.ApplyTo(licenseWindow);
            var ok = licenseWindow.ShowDialog();
            if (ok != true)
            {
                Shutdown();
                return;
            }
        }

        _main = new MainWindow(_settings, visualQa, screenshotPath);
        BrandIcon.ApplyTo(_main);
        MainWindow = _main;
        _main.CloseToTrayRequested += OnCloseToTray;
        OnControllerReady();

        var hide =
            !string.IsNullOrWhiteSpace(visualQa) ? false :
            _startupLaunch || _settings.StartMinimized;

        if (hide)
            _main.Hide();
        else
            _main.Show();
    }

    private void OnControllerReady()
    {
        if (_main is null || _tray is not null)
            return;

        _tray = new TrayIconService(
            _main.Shell,
            () => _main.ToggleConnectFromTrayAsync(),
            RestoreMainWindow,
            () => _main.NavigateTo(AppPage.Servers),
            () => _main.NavigateTo(AppPage.Settings),
            () => _main.NavigateTo(AppPage.Diagnostics),
            RequestExitAsync);
        _main.AttachTrayNotifications(_tray);
    }

    private void OnCloseToTray()
    {
        if (_main is null) return;
        _main.Hide();
        if (_settings.ShowNotifications && !_settings.TrayHintShown)
        {
            _settings.TrayHintShown = true;
            _settings.Save();
            _tray?.ShowBalloon(
                "Nyxveil",
                "Nyxveil продолжает работать в области уведомлений.");
        }
    }

    internal void RestoreMainWindow()
    {
        if (_exitRequested || _main is null)
            return;
        if (!_main.IsVisible)
            _main.Show();
        if (_main.WindowState == WindowState.Minimized)
            _main.WindowState = WindowState.Normal;
        _main.Activate();
        _main.Topmost = true;
        _main.Topmost = false;
        _main.Focus();
    }

    internal async Task RequestExitAsync()
    {
        if (_exitRequested)
            return;
        _exitRequested = true;

        try
        {
            if (_main is not null)
            {
                var ok = await _main.PrepareExitAsync(TimeSpan.FromSeconds(8)).ConfigureAwait(true);
                if (!ok)
                {
                    var confirm = MessageBox.Show(
                        "Не удалось корректно отключить VPN.\nВсё равно выйти?",
                        "Nyxveil",
                        MessageBoxButton.YesNo,
                        MessageBoxImage.Warning);
                    if (confirm != MessageBoxResult.Yes)
                    {
                        _exitRequested = false;
                        return;
                    }
                }

                _main.AllowClose();
                _tray?.Dispose();
                _tray = null;
                _main.Close();
            }
        }
        finally
        {
            Shutdown();
        }
    }

    private async void App_OnSessionEnding(object sender, SessionEndingCancelEventArgs e)
    {
        // Never block Windows shutdown/logoff with dialogs.
        try
        {
            if (_main is not null)
                await _main.PrepareExitAsync(TimeSpan.FromSeconds(3)).ConfigureAwait(true);
        }
        catch { /* ignore */ }
        finally
        {
            _exitRequested = true;
            _main?.AllowClose();
            _tray?.Dispose();
        }
    }

    protected override void OnExit(ExitEventArgs e)
    {
        SessionEnding -= App_OnSessionEnding;
        _tray?.Dispose();
        _singleInstance?.Dispose();
        base.OnExit(e);
    }
}
