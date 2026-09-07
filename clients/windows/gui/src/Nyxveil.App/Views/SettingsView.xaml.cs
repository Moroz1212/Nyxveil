using System.Windows;
using System.Windows.Controls;

namespace Nyxveil.App.Views;

public partial class SettingsView : UserControl
{
    public event Action? Back;
    public event Action? Save;

    public SettingsView() => InitializeComponent();
    private void Back_OnClick(object sender, RoutedEventArgs e) => Back?.Invoke();
    private void Save_OnClick(object sender, RoutedEventArgs e) => Save?.Invoke();
}
