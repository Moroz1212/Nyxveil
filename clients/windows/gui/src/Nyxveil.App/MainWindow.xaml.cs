using System.ComponentModel;
using System.IO;
using System.Windows;
using System.Windows.Input;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Nyxveil.App.Services;
using Nyxveil.App.ViewModels;
using Nyxveil.App.Views;
using Nyxveil.Client.Core;

namespace Nyxveil.App;

public partial class MainWindow : Window
{
    private readonly ClientSettings _settings;
    private readonly AppShellViewModel _shell;
    private readonly ClientSessionController _controller;
    private readonly string? _visualQaMode;
    private readonly string? _screenshotPath;

    private readonly HomeView _homeView = new();
    private readonly ServersView _serversView = new();
    private readonly SettingsView _settingsView = new();
    private readonly DiagnosticsView _diagnosticsView = new();
    private readonly LogsView _logsView = new();
    private readonly MoreView _moreView = new();

    public MainWindow(ClientSettings settings, string? visualQaMode = null, string? screenshotPath = null)
    {
        _settings = settings;
        _visualQaMode = visualQaMode;
        _screenshotPath = screenshotPath;
        _shell = new AppShellViewModel();
        _controller = new ClientSessionController(settings, _shell);

        InitializeComponent();
        DataContext = _shell;

        WireViews();
        _shell.PropertyChanged += Shell_OnPropertyChanged;
        ShowPage(AppPage.Home);

        Loaded += async (_, _) =>
        {
            if (!string.IsNullOrWhiteSpace(_visualQaMode))
            {
                // Layout-only path: skip service IPC so Status cannot overwrite QA paint.
                _controller.ApplyVisualQaSnapshot(_visualQaMode);
                await Dispatcher.InvokeAsync(() => { }, System.Windows.Threading.DispatcherPriority.Loaded);
                await Task.Delay(350);
                if (!string.IsNullOrWhiteSpace(_screenshotPath))
                {
                    SaveWindowPng(_screenshotPath);
                    Application.Current.Shutdown(0);
                }
                return;
            }

            await _controller.InitializeAsync();
            if (_settings.StartMinimized)
                WindowState = WindowState.Minimized;
            else if (_settings.AutoConnect && _shell.Server.Selected is not null)
                await _controller.ConnectSelectedAsync();
        };

        Closed += async (_, _) => await _controller.DisposeAsync();
    }

    private void SaveWindowPng(string path)
    {
        var dir = Path.GetDirectoryName(path);
        if (!string.IsNullOrEmpty(dir))
            Directory.CreateDirectory(dir);

        var dpi = VisualTreeHelper.GetDpi(this);
        var w = (int)Math.Ceiling(ActualWidth * dpi.DpiScaleX);
        var h = (int)Math.Ceiling(ActualHeight * dpi.DpiScaleY);
        if (w < 1 || h < 1) return;

        var rtb = new RenderTargetBitmap(w, h, dpi.PixelsPerInchX, dpi.PixelsPerInchY, PixelFormats.Pbgra32);
        rtb.Render(this);
        var encoder = new PngBitmapEncoder();
        encoder.Frames.Add(BitmapFrame.Create(rtb));
        using var fs = File.Create(path);
        encoder.Save(fs);
    }

    private void WireViews()
    {
        foreach (var v in new FrameworkElement[]
                 { _homeView, _serversView, _settingsView, _diagnosticsView, _logsView, _moreView })
            v.DataContext = _shell;

        _homeView.ConnectToggle += async () => await _controller.ToggleConnectAsync();
        _homeView.ChangeServer += () => Navigate(AppPage.Servers);
        _homeView.OpenSettings += () => Navigate(AppPage.Settings);
        _homeView.OpenDiagnostics += () => Navigate(AppPage.Diagnostics);
        _homeView.OpenLogs += async () =>
        {
            Navigate(AppPage.Logs);
            await _controller.EnsureLogsSubscriptionAsync(true);
        };
        _homeView.OpenMore += () => Navigate(AppPage.More);

        _serversView.Back += () => Navigate(AppPage.Home);
        _serversView.Selected += async item => await _controller.SelectServerAndMaybeReconnectAsync(item);

        _settingsView.Back += () => Navigate(AppPage.Home);
        _settingsView.Save += () =>
        {
            _controller.SaveSettingsFromVm();
            MessageBox.Show("Настройки сохранены.", "Nyxveil", MessageBoxButton.OK, MessageBoxImage.Information);
        };

        _diagnosticsView.Back += () => Navigate(AppPage.Home);
        _diagnosticsView.Copy += () =>
        {
            Clipboard.SetText(_controller.BuildDiagnosticsClipboard());
        };
        _diagnosticsView.OpenLogsFolder += ClientSessionController.OpenLogsFolder;

        _logsView.Back += async () =>
        {
            await _controller.EnsureLogsSubscriptionAsync(false);
            Navigate(AppPage.Home);
        };
        _logsView.OpenFolder += ClientSessionController.OpenLogsFolder;

        _moreView.Back += () => Navigate(AppPage.Home);
    }

    private void Shell_OnPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName == nameof(AppShellViewModel.Page))
            ShowPage(_shell.Page);
    }

    private void Navigate(AppPage page) => _shell.Page = page;

    private void ShowPage(AppPage page)
    {
        ContentHost.Content = page switch
        {
            AppPage.Servers => _serversView,
            AppPage.Settings => _settingsView,
            AppPage.Diagnostics => _diagnosticsView,
            AppPage.Logs => _logsView,
            AppPage.More => _moreView,
            _ => _homeView
        };
    }

    private void TitleBar_OnMinimize() => WindowState = WindowState.Minimized;

    private void TitleBar_OnMaximize() =>
        WindowState = WindowState == WindowState.Maximized ? WindowState.Normal : WindowState.Maximized;

    private void TitleBar_OnClose() => Close();

    private void TitleBar_OnDrag(object sender, MouseButtonEventArgs e)
    {
        if (e.ChangedButton == MouseButton.Left)
        {
            try { DragMove(); }
            catch { /* ignore */ }
        }
    }
}
