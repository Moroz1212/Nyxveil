using System.Windows;
using System.Windows.Controls;
using Nyxveil.App.ViewModels;

namespace Nyxveil.App.Views;

public partial class LogsView : UserControl
{
    public event Action? Back;
    public event Action? OpenFolder;

    public LogsView()
    {
        InitializeComponent();
        DataContextChanged += (_, _) =>
        {
            if (DataContext is AppShellViewModel shell)
                shell.Logs.PropertyChanged += (_, e) =>
                {
                    if (e.PropertyName == nameof(LogsViewModel.VisibleText) && shell.Logs.AutoScroll)
                        LogsScroll.ScrollToEnd();
                };
        };
    }

    private void Back_OnClick(object sender, RoutedEventArgs e) => Back?.Invoke();

    private void Copy_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is AppShellViewModel shell)
            Clipboard.SetText(shell.Logs.VisibleText ?? "");
    }

    private void Clear_OnClick(object sender, RoutedEventArgs e)
    {
        if (DataContext is AppShellViewModel shell)
            shell.Logs.ClearView();
    }

    private void Folder_OnClick(object sender, RoutedEventArgs e) => OpenFolder?.Invoke();
}
