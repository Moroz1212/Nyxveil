using System.Diagnostics;
using System.Net.Sockets;
using System.Windows;
using System.Windows.Media;
using System.Windows.Threading;
using Nyxveil.App.Services;
using Nyxveil.App.ViewModels;
using Nyxveil.Client.Core;
using Nyxveil.Client.Ipc;

namespace Nyxveil.App.Services;

/// <summary>Orchestrates IPC + catalog bootstrap into the shell ViewModel. No VPN logic in GUI.</summary>
public sealed class ClientSessionController : IAsyncDisposable
{
    private readonly ClientSettings _settings;
    private readonly SessionBootstrap _bootstrap;
    private readonly AppShellViewModel _shell;
    private readonly SpeedEmaCalculator _speed = new();
    private readonly DispatcherTimer _uiTick;
    private readonly DispatcherTimer _pingTick;
    private ServicePipeClient? _pipe;
    private bool _logsSubscribed;
    private bool _busy;
    private int? _lastPingMs;
    private SignedCatalogDto? _lastCatalog;

    public ClientSessionController(ClientSettings settings, AppShellViewModel shell)
    {
        _settings = settings;
        _shell = shell;
        _bootstrap = SessionBootstrap.FromSettings(settings);
        _bootstrap.SetCatalogDiagnostics(msg => AppendLocalLog("GUI", "CATALOG",
            msg.StartsWith("CATALOG ", StringComparison.Ordinal) ? msg["CATALOG ".Length..] : msg));

        _shell.Settings.Autostart = settings.Autostart;
        _shell.Settings.AutoConnect = settings.AutoConnect;
        _shell.Settings.StartMinimized = settings.StartMinimized;
        _shell.Settings.Notifications = settings.ShowNotifications;
        _shell.Logs.AutoScroll = settings.LogsAutoScroll;

        var ver = typeof(ClientSessionController).Assembly.GetName().Version;
        _shell.FooterVersion = ver is null ? "Nyxveil 1.1.1" : $"Nyxveil {ver.Major}.{ver.Minor}.{ver.Build}";
        _shell.More.About =
            $"{_shell.FooterVersion}\nПротокол: Nyxveil NVP/1\nCore: 1.0.0 (Frozen)\n" +
            $"CP: {settings.GetControlPlaneHost()}\n" +
            $"Лицензия: {(LicenseCredentialStore.Exists() ? "сохранена (CurrentUser)" : "нет")}";

        _uiTick = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _uiTick.Tick += (_, _) => OnUiTick();
        _uiTick.Start();

        _pingTick = new DispatcherTimer { Interval = TimeSpan.FromSeconds(15) };
        _pingTick.Tick += async (_, _) => await MeasurePingAsync();
        _pingTick.Start();
    }

    public AppShellViewModel Shell => _shell;

    public void ApplyVisualQaSnapshot(string mode)
    {
        // Layout-only QA: paints Home states without touching VPN dataplane.
        EnsureVisualQaServerFixture();
        switch (mode.Trim().ToLowerInvariant())
        {
            case "connected":
                _shell.Connection.EngineState = "Connected";
                _shell.Connection.ConnectedAtUnix = DateTimeOffset.UtcNow.AddMinutes(-12).AddSeconds(-36).ToUnixTimeSeconds();
                _shell.Connection.ErrorBrief = "";
                _shell.Stats.VpnIp = "10.66.0.25";
                _shell.Stats.Protocol = "Nyxveil NVP/1";
                _shell.Stats.Transport = "QUIC / UDP";
                _shell.Stats.Ping = "28 мс";
                _shell.Stats.Down = "92.4 Мбит/с";
                _shell.Stats.Up = "36.7 Мбит/с";
                _shell.Stats.Session = "00:12:36";
                _shell.Connection.SessionTimerText = "00:12:36";
                _shell.Server.QualityText = "Оптимальное соединение";
                _shell.Server.QualityOk = true;
                _shell.Diagnostics.Service = "OK";
                _shell.Diagnostics.Catalog = "OK";
                _shell.Diagnostics.Transport = "Connected";
                _shell.Diagnostics.Tunnel = "Active";
                _shell.Diagnostics.DataPlane = "Active";
                _shell.ServiceAlive = true;
                UpdateHealth();
                break;
            case "connecting":
                _shell.Connection.EngineState = "ConfiguringTunnel";
                _shell.Connection.ErrorBrief = "";
                _shell.Server.QualityText = "Сервер доступен";
                _shell.Server.QualityOk = true;
                _shell.Diagnostics.Service = "OK";
                _shell.ServiceAlive = true;
                UpdateHealth();
                break;
            case "error":
                _shell.Connection.EngineState = "Error";
                _shell.Connection.ErrorBrief = "Ошибка подключения";
                _shell.Diagnostics.Service = "OK";
                _shell.ServiceAlive = true;
                UpdateHealth();
                break;
            default:
                _shell.Connection.EngineState = "Disconnected";
                _shell.Connection.ConnectedAtUnix = 0;
                _shell.Connection.ErrorBrief = "";
                _shell.Stats.VpnIp = "—";
                _shell.Stats.Down = "—";
                _shell.Stats.Up = "—";
                _shell.Stats.Session = "00:00:00";
                _shell.Stats.Ping = "53 мс";
                _shell.Stats.Protocol = "Nyxveil NVP/1";
                _shell.Server.QualityText = "Сервер доступен";
                _shell.Server.QualityOk = true;
                _shell.Diagnostics.Service = "OK";
                _shell.ServiceAlive = true;
                UpdateHealth();
                break;
        }
    }

    private void EnsureVisualQaServerFixture()
    {
        if (_shell.Server.Selected is not null &&
            _shell.Server.Selected.TitleLine.Contains("Helsinki", StringComparison.OrdinalIgnoreCase))
            return;

        var item = new LocationItemViewModel
        {
            LocationId = "helsinki-qa",
            DisplayName = "Helsinki, Финляндия",
            City = "Helsinki",
            Country = "Финляндия",
            CountryCode = "FI",
            Healthy = true,
            CatalogLatencyMs = 28
        };
        _shell.Server.Locations.Clear();
        _shell.Server.Locations.Add(item);
        _shell.Server.Selected = item;
        _shell.Server.RefreshFiltered();
    }

    public async Task InitializeAsync()
    {
        await EnsurePipeAsync();
        await ReloadLocationsAsync();
        _ = MeasurePingAsync();
    }

    public async ValueTask DisposeAsync()
    {
        _uiTick.Stop();
        _pingTick.Stop();
        if (_pipe is not null)
        {
            try
            {
                if (_logsSubscribed && _pipe.IsConnected)
                    await _pipe.SendUnsubscribeLogsAsync();
            }
            catch { /* ignore */ }
            await _pipe.DisposeAsync();
        }
        _bootstrap.Dispose();
    }

    public void SaveSettingsFromVm()
    {
        _settings.Autostart = _shell.Settings.Autostart;
        _settings.AutoConnect = _shell.Settings.AutoConnect;
        _settings.StartMinimized = _shell.Settings.StartMinimized;
        _settings.ShowNotifications = _shell.Settings.Notifications;
        _settings.LogsAutoScroll = _shell.Logs.AutoScroll;
        _settings.Save();
    }

    public async Task EnsureLogsSubscriptionAsync(bool want)
    {
        if (_pipe is null || !_pipe.IsConnected) return;
        try
        {
            if (want && !_logsSubscribed)
            {
                await _pipe.SendSubscribeLogsAsync();
                _logsSubscribed = true;
            }
            else if (!want && _logsSubscribed)
            {
                await _pipe.SendUnsubscribeLogsAsync();
                _logsSubscribed = false;
            }
        }
        catch (Exception ex)
        {
            AppendLocalLog("GUI", "logs", ex.Message);
        }
    }

    public async Task ToggleConnectAsync()
    {
        if (_busy) return;
        var st = _shell.Connection.EngineState;
        if (ConnectionStateMapper.IsConnected(st) || st == "Reconnecting")
        {
            await DisconnectAsync();
            return;
        }
        if (ConnectionStateMapper.IsBusy(st))
        {
            try { if (_pipe?.IsConnected == true) await _pipe.SendCancelAsync(); } catch { /* ignore */ }
            return;
        }
        await ConnectSelectedAsync();
    }

    public async Task ConnectSelectedAsync()
    {
        var loc = _shell.Server.Selected;
        if (loc is null)
        {
            _shell.Connection.ErrorBrief = "Выберите сервер.";
            return;
        }
        await ConnectToAsync(loc.LocationId);
    }

    public async Task SelectServerAndMaybeReconnectAsync(LocationItemViewModel item)
    {
        _shell.Server.Selected = item;
        _settings.PreferredLocationId = item.LocationId;
        _settings.Save();
        _shell.Page = AppPage.Home;
        if (_shell.Connection.IsConnected || ConnectionStateMapper.IsBusy(_shell.Connection.EngineState))
        {
            await DisconnectAsync();
            await Task.Delay(400);
            await ConnectToAsync(item.LocationId);
        }
    }

    private async Task ConnectToAsync(string locationId)
    {
        if (_busy) return;
        _busy = true;
        _shell.Connection.CtaEnabled = false;
        _shell.Connection.ErrorBrief = "";
        _shell.Connection.EngineState = "ConnectingTransport";
        try
        {
            await EnsurePipeAsync();
            if (_pipe is null || !_pipe.IsConnected)
                throw new InvalidOperationException("Служба Nyxveil недоступна.");

            AppendLocalLog("GUI", "Connect", "location=" + locationId);
            var prep = await _bootstrap.PrepareConnectAsync(locationId);
            _settings.PreferredLocationId = locationId;
            _settings.Save();

            var waiter = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            void Handler(StatusSnapshotMessage s)
            {
                if (s.State is "Connected" or "connected") waiter.TrySetResult(true);
                else if (s.State is "Error" or "error" or "Disconnected" or "disconnected")
                    waiter.TrySetException(new InvalidOperationException(s.LastError ?? "Подключение не удалось."));
            }
            _pipe.StatusReceived += Handler;
            try
            {
                await _pipe.SendConnectAsync(
                    prep.DesiredLocationId,
                    prep.AccessTicket,
                    prep.SignedCatalogJson,
                    prep.CatalogKeys,
                    prep.DevicePrivateKey,
                    prep.ControlPlaneHost);
                await waiter.Task.WaitAsync(TimeSpan.FromMinutes(2));
            }
            finally
            {
                _pipe.StatusReceived -= Handler;
            }
        }
        catch (Exception ex)
        {
            _shell.Connection.EngineState = "Error";
            _shell.Connection.ErrorBrief = UserFacingError.Format(ex);
            AppendLocalLog("GUI", "Connect", "failed: " + ex.Message);
            UpdateHealth();
        }
        finally
        {
            _busy = false;
            _shell.Connection.CtaEnabled = true;
        }
    }

    public async Task DisconnectAsync()
    {
        _busy = true;
        _shell.Connection.CtaEnabled = false;
        try
        {
            AppendLocalLog("GUI", "Disconnect", "click");
            if (_pipe?.IsConnected == true)
                await _pipe.SendDisconnectAsync();
            _speed.Reset();
            _shell.Stats.Down = "—";
            _shell.Stats.Up = "—";
            _shell.Connection.ConnectedAtUnix = 0;
            _shell.Connection.SessionTimerText = "00:00:00";
            _shell.Stats.Session = "00:00:00";
        }
        catch (Exception ex)
        {
            _shell.Connection.ErrorBrief = UserFacingError.Format(ex);
        }
        finally
        {
            _busy = false;
            _shell.Connection.CtaEnabled = true;
        }
    }

    public string BuildDiagnosticsClipboard() =>
        _shell.Diagnostics.BuildCopyText(_settings, _shell.Connection, _shell.Stats, _shell.Server);

    public static void OpenLogsFolder()
    {
        var dir = System.IO.Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
            "Nyxveil", "Client", "logs");
        System.IO.Directory.CreateDirectory(dir);
        Process.Start(new ProcessStartInfo { FileName = dir, UseShellExecute = true });
    }

    private async Task EnsurePipeAsync()
    {
        try
        {
            _pipe ??= new ServicePipeClient();
            WirePipe(_pipe);
            if (!_pipe.IsConnected)
                await _pipe.ConnectAsync();
            _shell.ServiceAlive = true;
            _shell.Diagnostics.Service = "OK";
            AppendLocalLog("GUI", "pipe", "connected to service");
            UpdateHealth();
        }
        catch (Exception ex)
        {
            _shell.ServiceAlive = false;
            _shell.Diagnostics.Service = "Недоступна";
            AppendLocalLog("GUI", "pipe", "connect failed: " + ex.Message);
            UpdateHealth();
        }
    }

    private void WirePipe(ServicePipeClient pipe)
    {
        pipe.StatusReceived -= OnStatus;
        pipe.ErrorReceived -= OnPipeError;
        pipe.NeedAccessTicket -= OnNeedTicket;
        pipe.LogsSnapshotReceived -= OnLogsSnapshot;
        pipe.LogEventReceived -= OnLogEvent;
        pipe.Disconnected -= OnPipeDisconnected;
        pipe.StatusReceived += OnStatus;
        pipe.ErrorReceived += OnPipeError;
        pipe.NeedAccessTicket += OnNeedTicket;
        pipe.LogsSnapshotReceived += OnLogsSnapshot;
        pipe.LogEventReceived += OnLogEvent;
        pipe.Disconnected += OnPipeDisconnected;
    }

    private void OnPipeDisconnected()
    {
        Application.Current.Dispatcher.Invoke(() =>
        {
            _shell.ServiceAlive = false;
            _shell.Diagnostics.Service = "Недоступна";
            UpdateHealth();
        });
    }

    private void OnStatus(StatusSnapshotMessage s)
    {
        Application.Current.Dispatcher.Invoke(() => ApplyStatus(s));
    }

    private void ApplyStatus(StatusSnapshotMessage s)
    {
        _shell.Connection.EngineState = ConnectionStateMapper.Normalize(s.State);
        if (!string.IsNullOrWhiteSpace(s.LastError))
            _shell.Connection.ErrorBrief = UserFacingError.Map(s.LastError) ?? s.LastError;
        else if (_shell.Connection.EngineState != "Error")
            _shell.Connection.ErrorBrief = "";

        if (s.ConnectedAtUnix > 0)
            _shell.Connection.ConnectedAtUnix = s.ConnectedAtUnix;
        else if (!ConnectionStateMapper.IsConnected(s.State) && s.State != "Reconnecting")
            _shell.Connection.ConnectedAtUnix = 0;

        _shell.Stats.VpnIp = string.IsNullOrWhiteSpace(s.VpnIp) ? "—" : s.VpnIp!;
        _shell.Stats.Protocol = string.IsNullOrWhiteSpace(s.Protocol) ? "Nyxveil NVP/1" : $"Nyxveil {s.Protocol}";
        if (_shell.Stats.Protocol == "Nyxveil NVP/1" || s.Protocol == "NVP/1")
            _shell.Stats.Protocol = "Nyxveil NVP/1";
        _shell.Stats.Transport = FormatTransport(s.Transport);

        if (ConnectionStateMapper.IsConnected(s.State))
        {
            _speed.Sample(s.TxBytes, s.RxBytes, DateTime.UtcNow);
            _shell.Stats.Up = SpeedEmaCalculator.FormatMbps(_speed.TxMbps, true);
            _shell.Stats.Down = SpeedEmaCalculator.FormatMbps(_speed.RxMbps, true);
            _shell.Diagnostics.Transport = string.IsNullOrEmpty(s.Transport) ? "Connected" : s.Transport!;
            _shell.Diagnostics.Tunnel = string.IsNullOrEmpty(s.VpnIp) ? "Active" : $"Active ({s.VpnIp})";
            _shell.Diagnostics.DataPlane = "Active";
            _shell.Diagnostics.Routes = "Applied";
            _shell.Diagnostics.Dns = s.DnsServers is { Count: > 0 }
                ? string.Join(" / ", s.DnsServers)
                : "—";
            _shell.Diagnostics.Auth = "OK";
        }
        else
        {
            _speed.Reset();
            _shell.Stats.Up = "—";
            _shell.Stats.Down = "—";
            if (s.State == "Error")
            {
                _shell.Diagnostics.Transport = "Error";
                _shell.Diagnostics.DataPlane = "Stopped";
            }
            else if (s.State is "Disconnected" or null or "")
            {
                _shell.Diagnostics.Transport = "Idle";
                _shell.Diagnostics.Tunnel = "—";
                _shell.Diagnostics.Routes = "—";
                _shell.Diagnostics.DataPlane = "—";
                _shell.Diagnostics.Dns = "—";
            }
        }

        if (!string.IsNullOrEmpty(s.LocationId))
        {
            var match = _shell.Server.Locations.FirstOrDefault(l => l.LocationId == s.LocationId);
            if (match is not null)
                _shell.Server.Selected = match;
        }

        _shell.Diagnostics.Detail =
            $"State={s.State}\nNode={s.NodeId}\nLocation={s.LocationId}\n" +
            $"Transport={s.Transport}\nMTU={s.EffectiveMtu}\n" +
            $"TxBytes={s.TxBytes} RxBytes={s.RxBytes}\nLastError={s.LastError}";

        _shell.Server.RecomputeQuality(ConnectionStateMapper.IsConnected(s.State), _lastPingMs);
        UpdateHealth();
        RefreshTimer();
    }

    private static string FormatTransport(string? t)
    {
        if (string.IsNullOrWhiteSpace(t)) return "";
        if (t.Contains("quic", StringComparison.OrdinalIgnoreCase)) return "QUIC / UDP";
        if (t.Contains("tls", StringComparison.OrdinalIgnoreCase)) return "TLS / TCP";
        return t;
    }

    private void OnPipeError(string err)
    {
        Application.Current.Dispatcher.Invoke(() =>
        {
            _shell.Connection.ErrorBrief = UserFacingError.Map(err) ?? err;
            AppendLocalLog("GUI", "ipc_error", err);
            UpdateHealth();
        });
    }

    private async void OnNeedTicket(NeedAccessTicketMessage need)
    {
        try
        {
            var ticket = await _bootstrap.RefreshAccessTicketAsync(need.DesiredLocationId);
            if (_pipe?.IsConnected == true)
                await _pipe.SendAccessTicketAsync(need.RequestId, ticket);
        }
        catch (Exception ex)
        {
            AppendLocalLog("GUI", "ticket", "refresh failed: " + ex.Message);
        }
    }

    private void OnLogsSnapshot(LogsSnapshotMessage snap)
    {
        Application.Current.Dispatcher.Invoke(() =>
        {
            foreach (var e in snap.Entries)
                _shell.Logs.AddLine(FormatLine(e));
        });
    }

    private void OnLogEvent(LogLineDto entry)
    {
        Application.Current.Dispatcher.Invoke(() => _shell.Logs.AddLine(FormatLine(entry)));
    }

    private static string FormatLine(LogLineDto e) =>
        string.IsNullOrEmpty(e.Line)
            ? $"{e.Time} {e.Level} {e.Component} {e.Event} {e.Message}"
            : e.Line;

    private void AppendLocalLog(string component, string ev, string message)
    {
        var line = $"{DateTime.Now:HH:mm:ss} INFO {component} {ev} {message}";
        Application.Current?.Dispatcher?.Invoke(() => _shell.Logs.AddLine(line));
    }

    public async Task ReloadLocationsAsync()
    {
        try
        {
            var license = LicenseCredentialStore.Load();
            if (license is null) return;
            var (signed, _, _) = await _bootstrap.FetchAndVerifyCatalogAsync(license);
            _lastCatalog = signed;
            _shell.Diagnostics.Catalog = "OK";
            _shell.Diagnostics.ControlPlane = "OK";
            _shell.Diagnostics.Auth = LicenseCredentialStore.Exists() ? "OK" : "Нет лицензии";

            var byLoc = signed.Catalog.Nodes
                .Where(n => n.Enabled && !n.TestOnly)
                .GroupBy(n => n.LocationId)
                .ToDictionary(g => g.Key, g => g.OrderByDescending(n => n.Health.Healthy).First());

            _shell.Server.Locations.Clear();
            foreach (var loc in signed.Catalog.Locations.Where(l => l.Enabled))
            {
                byLoc.TryGetValue(loc.LocationId, out var node);
                var ep = node?.Endpoints?.FirstOrDefault();
                var city = FirstNonEmpty(loc.City, node?.City);
                var country = FirstNonEmpty(loc.Country, node?.Country);
                var title = BuildLocationTitle(loc.DisplayName, node?.DisplayName, city, country, loc.LocationId);
                var item = new LocationItemViewModel
                {
                    LocationId = loc.LocationId,
                    DisplayName = title,
                    City = city,
                    Country = country,
                    CountryCode = FlagService.InferFromLocation(loc.LocationId, country, loc.CountryCode),
                    Host = ep?.Host ?? node?.ServerName,
                    Port = ep?.Port ?? 0,
                    Healthy = node?.Health.Healthy ?? false,
                    CatalogLatencyMs = node?.Health.LatencyMs ?? 0,
                    Capacity = node?.Capacity ?? 0,
                    CurrentSessions = node?.CurrentSessions ?? 0
                };
                if (item.CatalogLatencyMs > 0)
                    item.PingText = $"{item.CatalogLatencyMs:0} мс";
                _shell.Server.Locations.Add(item);
            }
            _shell.Server.RefreshFiltered();

            LocationItemViewModel? prefer = null;
            if (!string.IsNullOrEmpty(_settings.PreferredLocationId))
                prefer = _shell.Server.Locations.FirstOrDefault(l => l.LocationId == _settings.PreferredLocationId);
            _shell.Server.Selected = prefer ?? _shell.Server.Locations.FirstOrDefault();
            _shell.Server.RecomputeQuality(_shell.Connection.IsConnected, _lastPingMs);
            UpdateHealth();
        }
        catch (Exception ex)
        {
            _shell.Diagnostics.Catalog = "Ошибка";
            AppendLocalLog("GUI", "catalog", ex.Message);
            UpdateHealth();
        }
    }

    private static string BuildLocationTitle(string? display, string? nodeDisplay, string city, string country, string locationId)
    {
        var d = FirstNonEmpty(display, nodeDisplay);
        if (!string.IsNullOrWhiteSpace(d) && d.Contains(','))
            return d;
        if (!string.IsNullOrWhiteSpace(city) && !string.IsNullOrWhiteSpace(country))
            return $"{city}, {country}";
        if (!string.IsNullOrWhiteSpace(d) && !string.IsNullOrWhiteSpace(country) && !d.Contains(country, StringComparison.OrdinalIgnoreCase))
            return $"{d}, {country}";
        return FirstNonEmpty(d, city, locationId);
    }

    private static string FirstNonEmpty(params string?[] parts)
    {
        foreach (var p in parts)
            if (!string.IsNullOrWhiteSpace(p)) return p!;
        return "";
    }

    private void OnUiTick()
    {
        RefreshTimer();
        if (_shell.Page == AppPage.Servers)
            _shell.Server.RefreshFiltered();
    }

    private void RefreshTimer()
    {
        if (_shell.Connection.ConnectedAtUnix <= 0 || !_shell.Connection.IsConnected)
        {
            if (!_shell.Connection.IsConnected)
            {
                _shell.Connection.SessionTimerText = "00:00:00";
                _shell.Stats.Session = "00:00:00";
            }
            return;
        }
        var start = DateTimeOffset.FromUnixTimeSeconds(_shell.Connection.ConnectedAtUnix);
        var elapsed = DateTimeOffset.UtcNow - start;
        if (elapsed < TimeSpan.Zero) elapsed = TimeSpan.Zero;
        var text = elapsed.ToString(@"hh\:mm\:ss");
        _shell.Connection.SessionTimerText = text;
        _shell.Stats.Session = text;
    }

    private async Task MeasurePingAsync()
    {
        var sel = _shell.Server.Selected;
        if (sel is null || string.IsNullOrWhiteSpace(sel.Host) || sel.Port <= 0)
        {
            _lastPingMs = null;
            _shell.Stats.Ping = "—";
            return;
        }
        try
        {
            var sw = Stopwatch.StartNew();
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(2));
            using var client = new TcpClient();
            await client.ConnectAsync(sel.Host, sel.Port, cts.Token);
            sw.Stop();
            _lastPingMs = (int)sw.ElapsedMilliseconds;
            var text = $"{_lastPingMs} мс";
            Application.Current.Dispatcher.Invoke(() =>
            {
                _shell.Stats.Ping = text;
                sel.PingText = text;
                _shell.Server.RecomputeQuality(_shell.Connection.IsConnected, _lastPingMs);
            });
        }
        catch
        {
            _lastPingMs = null;
            Application.Current.Dispatcher.Invoke(() =>
            {
                if (!_shell.Connection.IsConnected)
                    _shell.Stats.Ping = "—";
                _shell.Server.RecomputeQuality(_shell.Connection.IsConnected, null);
            });
        }
    }

    private void UpdateHealth()
    {
        if (!_shell.ServiceAlive)
        {
            _shell.HealthText = "Требуется внимание";
            _shell.HealthBrush = (Brush)Application.Current.FindResource("ErrRed");
            return;
        }
        if (_shell.Connection.EngineState == "Error" || !string.IsNullOrWhiteSpace(_shell.Connection.ErrorBrief)
            && _shell.Connection.EngineState != "Connected")
        {
            if (_shell.Connection.EngineState == "Error")
            {
                _shell.HealthText = "Требуется внимание";
                _shell.HealthBrush = (Brush)Application.Current.FindResource("ErrRed");
                return;
            }
            _shell.HealthText = "Есть предупреждения";
            _shell.HealthBrush = (Brush)Application.Current.FindResource("WarnAmber");
            return;
        }
        if (_shell.Diagnostics.Catalog == "Ошибка")
        {
            _shell.HealthText = "Есть предупреждения";
            _shell.HealthBrush = (Brush)Application.Current.FindResource("WarnAmber");
            return;
        }
        _shell.HealthText = "Все системы в норме";
        _shell.HealthBrush = (Brush)Application.Current.FindResource("OkGreen");
    }
}
