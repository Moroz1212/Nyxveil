using System.Windows;
using System.Windows.Media;
using Nyxveil.Client.Core;
using Nyxveil.Client.Ipc;

namespace Nyxveil.App;

public partial class MainWindow : Window
{
    private readonly ClientSettings _settings;
    private readonly SessionBootstrap _bootstrap;
    private ServicePipeClient? _pipe;
    private bool _busy;
    private string _connectedState = "disconnected";

    public MainWindow(ClientSettings settings)
    {
        _settings = settings;
        _bootstrap = SessionBootstrap.FromSettings(settings);
        InitializeComponent();
        AutostartBox.IsChecked = _settings.Autostart;
        RefreshDiagnostics("Disconnected", "", "", "", "");
        Loaded += async (_, _) => await LoadLocationsAsync();
        Closed += async (_, _) =>
        {
            if (_pipe is not null)
                await _pipe.DisposeAsync();
            _bootstrap.Dispose();
        };
    }

    private void AutostartBox_OnChanged(object sender, RoutedEventArgs e)
    {
        _settings.Autostart = AutostartBox.IsChecked == true;
        _settings.Save();
    }

    private void RefreshDiagnostics(string state, string node, string loc, string transport, string lastErr)
    {
        DiagText.Text =
            $"Client: 1.0.0\nCore: 1.0.0\nProtocol: NVP/1\n" +
            $"CP: {_settings.GetControlPlaneHost()}\n" +
            $"License: {(LicenseCredentialStore.Exists() ? "сохранена (CurrentUser)" : "нет")}\n" +
            $"State: {state}\nLocation: {loc}\nNode: {node}\nTransport: {transport}\n" +
            $"LastError: {lastErr}";
    }

    private async Task LoadLocationsAsync()
    {
        try
        {
            LocationBox.Items.Clear();
            LocationBox.Items.Add("Выберите локацию");
            var locs = await _bootstrap.LoadEnabledLocationsAsync();
            foreach (var loc in locs)
                LocationBox.Items.Add(new LocationItem(loc.LocationId, loc.DisplayName ?? loc.LocationId));
            LocationBox.SelectedIndex = 0;
            if (!string.IsNullOrEmpty(_settings.PreferredLocationId))
            {
                for (var i = 1; i < LocationBox.Items.Count; i++)
                {
                    if (LocationBox.Items[i] is LocationItem li && li.Id == _settings.PreferredLocationId)
                    {
                        LocationBox.SelectedIndex = i;
                        break;
                    }
                }
            }
            StatusText.Text = "Статус: Отключено";
            DetailText.Text = "Каталог загружен.";
        }
        catch (Exception ex)
        {
            ErrorText.Text = ex.Message;
            DetailText.Text = "Не удалось загрузить каталог. Проверьте лицензию и Control Plane.";
        }
    }

    private async void ConnectButton_OnClick(object sender, RoutedEventArgs e)
    {
        if (_busy)
            return;
        ErrorText.Text = "";

        if (_connectedState is "connected" or "connecting")
        {
            await DisconnectAsync();
            return;
        }

        if (LocationBox.SelectedItem is not LocationItem loc)
        {
            ErrorText.Text = "Выберите локацию.";
            return;
        }

        _busy = true;
        ConnectButton.IsEnabled = false;
        StatusText.Text = "Статус: Подключение…";
        StatusDot.Fill = (Brush)FindResource("Accent");
        DetailText.Text = "Получение ticket и каталога…";
        try
        {
            var prep = await _bootstrap.PrepareConnectAsync(loc.Id);
            _settings.PreferredLocationId = loc.Id;
            _settings.Save();

            _pipe ??= new ServicePipeClient();
            _pipe.StatusReceived -= OnStatus;
            _pipe.ErrorReceived -= OnPipeError;
            _pipe.NeedAccessTicket -= OnNeedTicket;
            _pipe.StatusReceived += OnStatus;
            _pipe.ErrorReceived += OnPipeError;
            _pipe.NeedAccessTicket += OnNeedTicket;

            if (!_pipe.IsConnected)
            {
                DetailText.Text = "Соединение со службой Nyxveil…";
                await _pipe.ConnectAsync();
            }

            DetailText.Text = "Открытие сессии (Frozen Connector)…";
            await _pipe.SendConnectAsync(
                prep.DesiredLocationId,
                prep.AccessTicket,
                prep.SignedCatalogJson,
                prep.CatalogKeys,
                prep.DevicePrivateKey,
                prep.ControlPlaneHost);

            _connectedState = "connecting";
            ConnectButton.Content = "ОТКЛЮЧИТЬ";
        }
        catch (Exception ex)
        {
            ErrorText.Text = ex.Message;
            StatusText.Text = "Статус: Ошибка";
            StatusDot.Fill = new SolidColorBrush(Color.FromRgb(0x6B, 0x72, 0x80));
            _connectedState = "disconnected";
            ConnectButton.Content = "ПОДКЛЮЧИТЬ";
        }
        finally
        {
            _busy = false;
            ConnectButton.IsEnabled = true;
        }
    }

    private async Task DisconnectAsync()
    {
        _busy = true;
        try
        {
            if (_pipe is { IsConnected: true })
                await _pipe.SendDisconnectAsync();
        }
        catch (Exception ex)
        {
            ErrorText.Text = ex.Message;
        }
        finally
        {
            _connectedState = "disconnected";
            ConnectButton.Content = "ПОДКЛЮЧИТЬ";
            StatusText.Text = "Статус: Отключено";
            StatusDot.Fill = new SolidColorBrush(Color.FromRgb(0x6B, 0x72, 0x80));
            DetailText.Text = "";
            _busy = false;
        }
    }

    private void OnStatus(StatusSnapshotMessage s)
    {
        Dispatcher.Invoke(() =>
        {
            var state = (s.State ?? "").ToLowerInvariant();
            if (state.Contains("connected") && !state.Contains("disconnect"))
            {
                _connectedState = "connected";
                StatusText.Text = "Статус: Подключено";
                StatusDot.Fill = (Brush)FindResource("Accent");
                DetailText.Text = string.IsNullOrEmpty(s.NodeId) ? "" : $"Узел: {s.NodeId}";
                ConnectButton.Content = "ОТКЛЮЧИТЬ";
                RefreshDiagnostics(s.State ?? "Connected", s.NodeId ?? "", s.LocationId ?? "", s.Transport ?? "", s.LastError ?? "");
            }
            else if (state.Contains("error") || !string.IsNullOrEmpty(s.LastError))
            {
                ErrorText.Text = UserFacingError.Map(s.LastError) ?? "Ошибка подключения";
                StatusText.Text = "Статус: Ошибка";
                RefreshDiagnostics(s.State ?? "Error", s.NodeId ?? "", s.LocationId ?? "", s.Transport ?? "", s.LastError ?? "");
            }
            else if (state.Contains("disconnect"))
            {
                _connectedState = "disconnected";
                StatusText.Text = "Статус: Отключено";
                ConnectButton.Content = "ПОДКЛЮЧИТЬ";
                RefreshDiagnostics("Disconnected", "", "", "", "");
            }
            else
            {
                StatusText.Text = "Статус: " + (s.State ?? "…");
                DetailText.Text = s.LastError ?? DetailText.Text;
                RefreshDiagnostics(s.State ?? "", s.NodeId ?? "", s.LocationId ?? "", s.Transport ?? "", s.LastError ?? "");
            }
        });
    }

    private void OnPipeError(string err)
    {
        Dispatcher.Invoke(() =>
        {
            ErrorText.Text = err;
            StatusText.Text = "Статус: Ошибка";
            _connectedState = "disconnected";
            ConnectButton.Content = "ПОДКЛЮЧИТЬ";
        });
    }

    private async void OnNeedTicket(NeedAccessTicketMessage need)
    {
        try
        {
            var loc = need.DesiredLocationId;
            if (string.IsNullOrWhiteSpace(loc))
                throw new InvalidOperationException("need_access_ticket без location_id.");
            if (string.IsNullOrWhiteSpace(need.RequestId))
                throw new InvalidOperationException("need_access_ticket без request_id.");
            var ticket = await _bootstrap.RefreshAccessTicketAsync(loc);
            if (_pipe is not null)
                await _pipe.SendAccessTicketAsync(need.RequestId, ticket);
        }
        catch (Exception ex)
        {
            Dispatcher.Invoke(() => ErrorText.Text = ex.Message);
        }
    }

    private sealed record LocationItem(string Id, string Name)
    {
        public override string ToString() => Name;
    }
}
