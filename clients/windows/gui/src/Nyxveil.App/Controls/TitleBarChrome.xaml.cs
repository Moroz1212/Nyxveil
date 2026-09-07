using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;

namespace Nyxveil.App.Controls;

public partial class TitleBarChrome : UserControl
{
    public event Action? MinimizeRequested;
    public event Action? MaximizeRequested;
    public event Action? CloseRequested;
    public event MouseButtonEventHandler? DragRequested;

    public TitleBarChrome()
    {
        InitializeComponent();
    }

    private void DragArea_OnMouseLeftButtonDown(object sender, MouseButtonEventArgs e)
    {
        DragRequested?.Invoke(sender, e);
        if (e.ClickCount == 2)
            MaximizeRequested?.Invoke();
    }

    private void Minimize_OnClick(object sender, RoutedEventArgs e) => MinimizeRequested?.Invoke();
    private void Maximize_OnClick(object sender, RoutedEventArgs e) => MaximizeRequested?.Invoke();
    private void Close_OnClick(object sender, RoutedEventArgs e) => CloseRequested?.Invoke();
}
