using System.Collections.ObjectModel;
using System.Windows.Media;
using Nyxveil.App.Services;
using Nyxveil.Client.Core;

namespace Nyxveil.App.ViewModels;

public enum AppPage
{
    Home,
    Servers,
    Settings,
    Diagnostics,
    Logs,
    More
}

public sealed class AppShellViewModel : ObservableObject
{
    private AppPage _page = AppPage.Home;
    public AppPage Page
    {
        get => _page;
        set { if (Set(ref _page, value)) Raise(nameof(IsHome)); }
    }
    public bool IsHome => Page == AppPage.Home;

    public ConnectionViewModel Connection { get; } = new();
    public ServerViewModel Server { get; } = new();
    public StatsViewModel Stats { get; } = new();
    public DiagnosticsViewModel Diagnostics { get; } = new();
    public LogsViewModel Logs { get; } = new();
    public SettingsViewModel Settings { get; } = new();
    public MoreViewModel More { get; } = new();

    private string _footerVersion = "Nyxveil 1.1.2";
    public string FooterVersion { get => _footerVersion; set => Set(ref _footerVersion, value); }

    private string _healthText = "Служба недоступна";
    public string HealthText { get => _healthText; set => Set(ref _healthText, value); }

    private Brush _healthBrush = new SolidColorBrush(Color.FromRgb(0xE6, 0xB8, 0x4D));
    public Brush HealthBrush { get => _healthBrush; set => Set(ref _healthBrush, value); }

    private bool _serviceAlive;
    public bool ServiceAlive { get => _serviceAlive; set => Set(ref _serviceAlive, value); }
}

public sealed class ConnectionViewModel : ObservableObject
{
    private string _engineState = "Disconnected";
    public string EngineState
    {
        get => _engineState;
        set
        {
            if (!Set(ref _engineState, value)) return;
            StatusLabel = ConnectionStateMapper.ToStatusLabel(value);
            RingVisual = ConnectionStateMapper.ToRingVisual(value);
            IsConnected = ConnectionStateMapper.IsConnected(value);
            IsBusy = ConnectionStateMapper.IsBusy(value);
            CtaText = IsConnected
                ? "Отключить"
                : (IsBusy ? "Отмена" : "Подключить");
            if (value is "Disconnected" or "Error")
                SessionTimerText = "00:00:00";
        }
    }

    private string _statusLabel = "Отключено";
    public string StatusLabel { get => _statusLabel; set => Set(ref _statusLabel, value); }

    private string _ringVisual = "Disconnected";
    public string RingVisual { get => _ringVisual; set => Set(ref _ringVisual, value); }

    private string _sessionTimerText = "00:00:00";
    public string SessionTimerText { get => _sessionTimerText; set => Set(ref _sessionTimerText, value); }

    private string _ctaText = "Подключить";
    public string CtaText { get => _ctaText; set => Set(ref _ctaText, value); }

    private string _errorBrief = "";
    public string ErrorBrief { get => _errorBrief; set => Set(ref _errorBrief, value); }

    private bool _isConnected;
    public bool IsConnected { get => _isConnected; set => Set(ref _isConnected, value); }

    private bool _isBusy;
    public bool IsBusy { get => _isBusy; set => Set(ref _isBusy, value); }

    private bool _ctaEnabled = true;
    public bool CtaEnabled { get => _ctaEnabled; set => Set(ref _ctaEnabled, value); }

    public long ConnectedAtUnix { get; set; }
}

public sealed class LocationItemViewModel : ObservableObject
{
    public required string LocationId { get; init; }
    public required string DisplayName { get; init; }
    public required string City { get; init; }
    public required string Country { get; init; }
    public required string CountryCode { get; init; }
    public string? Host { get; init; }
    public int Port { get; init; }
    public bool Healthy { get; init; }
    public double CatalogLatencyMs { get; init; }
    public int Capacity { get; init; }
    public int CurrentSessions { get; init; }

    private bool _selected;
    public bool Selected { get => _selected; set => Set(ref _selected, value); }

    private string _pingText = "—";
    public string PingText { get => _pingText; set => Set(ref _pingText, value); }

    private string _loadText = "—";
    public string LoadText { get => _loadText; set => Set(ref _loadText, value); }

    public string TitleLine => string.IsNullOrWhiteSpace(DisplayName)
        ? $"{City}, {Country}".Trim(' ', ',')
        : DisplayName;
}

public sealed class ServerViewModel : ObservableObject
{
    public ObservableCollection<LocationItemViewModel> Locations { get; } = new();

    private LocationItemViewModel? _selected;
    public LocationItemViewModel? Selected
    {
        get => _selected;
        set
        {
            if (!Set(ref _selected, value)) return;
            foreach (var l in Locations)
                l.Selected = ReferenceEquals(l, value);
            Raise(nameof(CurrentTitle));
            Raise(nameof(QualityText));
            Raise(nameof(CountryCode));
        }
    }

    private string _search = "";
    public string Search
    {
        get => _search;
        set { if (Set(ref _search, value)) RefreshFiltered(); }
    }

    public ObservableCollection<LocationItemViewModel> VisibleLocations { get; } = new();

    public void RefreshFiltered()
    {
        VisibleLocations.Clear();
        IEnumerable<LocationItemViewModel> q = Locations;
        if (!string.IsNullOrWhiteSpace(Search))
        {
            q = Locations.Where(l =>
                l.TitleLine.Contains(Search, StringComparison.OrdinalIgnoreCase) ||
                l.Country.Contains(Search, StringComparison.OrdinalIgnoreCase) ||
                l.LocationId.Contains(Search, StringComparison.OrdinalIgnoreCase));
        }
        foreach (var l in q)
            VisibleLocations.Add(l);
    }

    public string CurrentTitle => Selected?.TitleLine ?? "Сервер не выбран";
    public string CountryCode => Selected?.CountryCode ?? "";

    private string _qualityText = "—";
    public string QualityText { get => _qualityText; set => Set(ref _qualityText, value); }

    private bool _qualityOk;
    public bool QualityOk { get => _qualityOk; set => Set(ref _qualityOk, value); }

    public void RecomputeQuality(bool connected, int? measuredPingMs)
    {
        if (Selected is null)
        {
            QualityText = "—";
            QualityOk = false;
            return;
        }

        if (Selected.Capacity > 0)
        {
            var load = 100.0 * Selected.CurrentSessions / Selected.Capacity;
            Selected.LoadText = $"{load:0}%";
        }
        else
            Selected.LoadText = "—";

        if (connected)
        {
            if (measuredPingMs is > 0 and < 80 && Selected.Healthy)
            {
                QualityText = "Оптимальное соединение";
                QualityOk = true;
            }
            else
            {
                QualityText = "Соединение активно";
                QualityOk = true;
            }
            return;
        }

        if (Selected.Healthy && (measuredPingMs is > 0 and < 120 || Selected.CatalogLatencyMs is > 0 and < 120))
        {
            QualityText = "Сервер доступен";
            QualityOk = true;
        }
        else if (Selected.Healthy)
        {
            QualityText = "Сервер доступен";
            QualityOk = true;
        }
        else
        {
            QualityText = "Нет данных";
            QualityOk = false;
        }
    }
}

public sealed class StatsViewModel : ObservableObject
{
    private string _session = "00:00:00";
    public string Session { get => _session; set => Set(ref _session, value); }

    private string _ping = "—";
    public string Ping { get => _ping; set => Set(ref _ping, value); }

    private string _down = "—";
    public string Down { get => _down; set => Set(ref _down, value); }

    private string _up = "—";
    public string Up { get => _up; set => Set(ref _up, value); }

    private string _vpnIp = "—";
    public string VpnIp { get => _vpnIp; set => Set(ref _vpnIp, value); }

    private string _protocol = "Nyxveil NVP/1";
    public string Protocol { get => _protocol; set => Set(ref _protocol, value); }

    private string _transport = "";
    public string Transport { get => _transport; set => Set(ref _transport, value); }
}

public sealed class DiagnosticsViewModel : ObservableObject
{
    private string _service = "—";
    public string Service { get => _service; set => Set(ref _service, value); }
    private string _controlPlane = "—";
    public string ControlPlane { get => _controlPlane; set => Set(ref _controlPlane, value); }
    private string _catalog = "—";
    public string Catalog { get => _catalog; set => Set(ref _catalog, value); }
    private string _auth = "—";
    public string Auth { get => _auth; set => Set(ref _auth, value); }
    private string _transport = "—";
    public string Transport { get => _transport; set => Set(ref _transport, value); }
    private string _tunnel = "—";
    public string Tunnel { get => _tunnel; set => Set(ref _tunnel, value); }
    private string _routes = "—";
    public string Routes { get => _routes; set => Set(ref _routes, value); }
    private string _dns = "—";
    public string Dns { get => _dns; set => Set(ref _dns, value); }
    private string _dataPlane = "—";
    public string DataPlane { get => _dataPlane; set => Set(ref _dataPlane, value); }
    private string _detail = "";
    public string Detail { get => _detail; set => Set(ref _detail, value); }

    public string BuildCopyText(ClientSettings settings, ConnectionViewModel conn, StatsViewModel stats, ServerViewModel server) =>
        $"Nyxveil Diagnostics\n" +
        $"Version: {conn.EngineState}\n" +
        $"CP: {settings.GetControlPlaneHost()}\n" +
        $"Service: {Service}\nCatalog: {Catalog}\nAuth: {Auth}\n" +
        $"Transport: {Transport}\nTunnel: {Tunnel}\nDNS: {Dns}\n" +
        $"DataPlane: {DataPlane}\nVPN IP: {stats.VpnIp}\n" +
        $"Location: {server.Selected?.LocationId}\nNode protocol: {stats.Protocol}\n" +
        $"LastError: {conn.ErrorBrief}\n{Detail}";
}

public sealed class LogsViewModel : ObservableObject
{
    public ObservableCollection<string> Lines { get; } = new();
    private string _filter = "";
    public string Filter { get => _filter; set { if (Set(ref _filter, value)) Raise(nameof(VisibleText)); } }
    private string _severity = "ALL";
    public string Severity { get => _severity; set { if (Set(ref _severity, value)) Raise(nameof(VisibleText)); } }
    private bool _autoScroll = true;
    public bool AutoScroll { get => _autoScroll; set => Set(ref _autoScroll, value); }

    public string VisibleText
    {
        get
        {
            IEnumerable<string> q = Lines;
            if (!string.Equals(Severity, "ALL", StringComparison.OrdinalIgnoreCase))
                q = q.Where(l => l.Contains($" {Severity} ", StringComparison.OrdinalIgnoreCase)
                                 || l.StartsWith(Severity + " ", StringComparison.OrdinalIgnoreCase));
            if (!string.IsNullOrWhiteSpace(Filter))
                q = q.Where(l => l.Contains(Filter, StringComparison.OrdinalIgnoreCase));
            return string.Join(Environment.NewLine, q);
        }
    }

    public void AddLine(string line)
    {
        Lines.Add(line);
        while (Lines.Count > 2000)
            Lines.RemoveAt(0);
        Raise(nameof(VisibleText));
    }

    public void ClearView()
    {
        Lines.Clear();
        Raise(nameof(VisibleText));
    }
}

public sealed class SettingsViewModel : ObservableObject
{
    private bool _autostart;
    public bool Autostart { get => _autostart; set => Set(ref _autostart, value); }
    private bool _autoConnect;
    public bool AutoConnect { get => _autoConnect; set => Set(ref _autoConnect, value); }
    private bool _startMinimized;
    public bool StartMinimized { get => _startMinimized; set => Set(ref _startMinimized, value); }
    private bool _notifications = true;
    public bool Notifications { get => _notifications; set => Set(ref _notifications, value); }
    public string Appearance => "Dark";
}

public sealed class MoreViewModel : ObservableObject
{
    private string _about =
        "Nyxveil Windows Client\nПротокол: Nyxveil NVP/1\nCore: 1.0.0 (Frozen)";
    public string About { get => _about; set => Set(ref _about, value); }
}
