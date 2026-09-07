using System.Windows;
using System.Windows.Controls;
using Nyxveil.App.ViewModels;

namespace Nyxveil.App.Views;

public partial class HomeView : UserControl
{
    public event Action? ChangeServer;
    public event Action? ConnectToggle;
    public event Action? OpenSettings;
    public event Action? OpenDiagnostics;
    public event Action? OpenLogs;
    public event Action? OpenMore;

    public HomeView()
    {
        InitializeComponent();
    }

    private void ChangeServer_OnClick(object sender, RoutedEventArgs e) => ChangeServer?.Invoke();
    private void Cta_OnClick(object sender, RoutedEventArgs e) => ConnectToggle?.Invoke();
    private void NavSettings_OnClick(object sender, RoutedEventArgs e) => OpenSettings?.Invoke();
    private void NavDiag_OnClick(object sender, RoutedEventArgs e) => OpenDiagnostics?.Invoke();
    private void NavLogs_OnClick(object sender, RoutedEventArgs e) => OpenLogs?.Invoke();
    private void NavMore_OnClick(object sender, RoutedEventArgs e) => OpenMore?.Invoke();

    private void CopyIp_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is AppShellViewModel shell &&
            !string.IsNullOrWhiteSpace(shell.Stats.VpnIp) &&
            shell.Stats.VpnIp != "—")
        {
            Clipboard.SetText(shell.Stats.VpnIp);
        }
    }
}
