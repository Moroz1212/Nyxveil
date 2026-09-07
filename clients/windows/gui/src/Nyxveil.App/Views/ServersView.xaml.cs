using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using Nyxveil.App.ViewModels;

namespace Nyxveil.App.Views;

public partial class ServersView : UserControl
{
    public event Action? Back;
    public event Action<LocationItemViewModel>? Selected;

    public ServersView() => InitializeComponent();

    private void Back_OnClick(object sender, RoutedEventArgs e) => Back?.Invoke();

    private void List_OnDoubleClick(object sender, MouseButtonEventArgs e) => ChooseSelected();

    private void Select_OnClick(object sender, RoutedEventArgs e) => ChooseSelected();

    private void ChooseSelected()
    {
        if (ServerList.SelectedItem is LocationItemViewModel item)
            Selected?.Invoke(item);
    }
}
