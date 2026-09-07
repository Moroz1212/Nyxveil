using System.Windows;
using System.Windows.Controls;

namespace Nyxveil.App.Views;

public partial class DiagnosticsView : UserControl
{
    public event Action? Back;
    public event Action? Copy;
    public event Action? OpenLogsFolder;

    public DiagnosticsView() => InitializeComponent();
    private void Back_OnClick(object sender, RoutedEventArgs e) => Back?.Invoke();
    private void Copy_OnClick(object sender, RoutedEventArgs e) => Copy?.Invoke();
    private void OpenLogs_OnClick(object sender, RoutedEventArgs e) => OpenLogsFolder?.Invoke();
}
