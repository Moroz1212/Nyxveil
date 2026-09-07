using System.Windows;
using System.Windows.Controls;

namespace Nyxveil.App.Views;

public partial class MoreView : UserControl
{
    public event Action? Back;
    public MoreView() => InitializeComponent();
    private void Back_OnClick(object sender, RoutedEventArgs e) => Back?.Invoke();
}
