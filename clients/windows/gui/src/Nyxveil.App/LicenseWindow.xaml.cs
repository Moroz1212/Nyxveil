using System.Windows;
using System.Windows.Input;
using Nyxveil.Client.Core;

namespace Nyxveil.App;

public partial class LicenseWindow : Window
{
    private readonly ClientSettings _settings;
    private bool _busy;

    public LicenseWindow(ClientSettings settings)
    {
        InitializeComponent();
        _settings = settings;
        CpUrlBox.Text = settings.ControlPlaneBaseUrl;
    }

    private async void ActivateButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (_busy)
            return;

        ErrorText.Text = "";
        var url = CpUrlBox.Text.Trim();
        var token = LicenseBox.Text.Trim();
        if (string.IsNullOrWhiteSpace(url))
        {
            ErrorText.Text = "Укажите адрес Control Plane.";
            return;
        }

        if (string.IsNullOrWhiteSpace(token))
        {
            ErrorText.Text = "Введите лицензионный ключ.";
            return;
        }

        _settings.ControlPlaneBaseUrl = url;
        SetBusy(true);

        try
        {
            using var bootstrap = SessionBootstrap.FromSettings(_settings);
            await bootstrap.ActivateLicenseAsync(token).ConfigureAwait(true);
            DialogResult = true;
            Close();
        }
        catch (Exception ex)
        {
            ErrorText.Text = UserFacingError.FormatLicense(ex);
            SetBusy(false);
        }
    }

    private void SetBusy(bool busy)
    {
        _busy = busy;
        CpUrlBox.IsEnabled = !busy;
        LicenseBox.IsEnabled = !busy;
        ActivateButton.IsEnabled = !busy;
        ActivateButton.Content = busy ? "Проверка..." : "Продолжить";
        BusyPanel.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
    }

    private void TitleBar_OnMinimize() => WindowState = WindowState.Minimized;

    private void TitleBar_OnMaximize() { /* fixed activation layout */ }

    private void TitleBar_OnClose()
    {
        DialogResult = false;
        Close();
    }

    private void TitleBar_OnDrag(object sender, MouseButtonEventArgs e)
    {
        if (e.ChangedButton == MouseButton.Left)
        {
            try { DragMove(); }
            catch { /* ignore */ }
        }
    }
}
