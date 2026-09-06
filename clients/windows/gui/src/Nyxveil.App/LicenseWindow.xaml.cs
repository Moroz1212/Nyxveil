using System.Windows;
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
        _busy = true;
        ActivateButton.IsEnabled = false;
        ActivateButton.Content = "Проверка…";

        try
        {
            using var bootstrap = SessionBootstrap.FromSettings(_settings);
            await bootstrap.ActivateLicenseAsync(token).ConfigureAwait(true);
            DialogResult = true;
            Close();
        }
        catch (Exception ex)
        {
            ErrorText.Text = UserFacingError.Format(ex);
            ActivateButton.IsEnabled = true;
            ActivateButton.Content = "ПРОДОЛЖИТЬ";
            _busy = false;
        }
    }
}
