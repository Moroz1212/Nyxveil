using System.Drawing;
using System.IO;
using System.Windows.Forms;
using Nyxveil.App.Services;
using Nyxveil.App.ViewModels;

namespace Nyxveil.App;

/// <summary>System tray integration using the approved Nyxveil.ico (via EXE / file).</summary>
internal sealed class TrayIconService : IDisposable
{
    private readonly NotifyIcon _notify;
    private readonly AppShellViewModel _shell;
    private readonly Func<Task> _connectOrDisconnect;
    private readonly Action _openMain;
    private readonly Action _openServers;
    private readonly Action _openSettings;
    private readonly Action _openDiagnostics;
    private readonly Func<Task> _exitAsync;
    private ToolStripMenuItem? _connectItem;
    private bool _disposed;

    public TrayIconService(
        AppShellViewModel shell,
        Func<Task> connectOrDisconnect,
        Action openMain,
        Action openServers,
        Action openSettings,
        Action openDiagnostics,
        Func<Task> exitAsync)
    {
        _shell = shell;
        _connectOrDisconnect = connectOrDisconnect;
        _openMain = openMain;
        _openServers = openServers;
        _openSettings = openSettings;
        _openDiagnostics = openDiagnostics;
        _exitAsync = exitAsync;

        _notify = new NotifyIcon
        {
            Icon = LoadBrandIcon(),
            Visible = true,
            Text = "Nyxveil — Отключено"
        };
        _notify.DoubleClick += (_, _) => _openMain();
        _notify.ContextMenuStrip = BuildMenu();
        _shell.PropertyChanged += Shell_OnPropertyChanged;
        _shell.Connection.PropertyChanged += (_, _) => UpdateUi();
        _shell.Server.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName is nameof(ServerViewModel.Selected) or nameof(ServerViewModel.CurrentTitle))
                UpdateUi();
        };
        UpdateUi();
    }

    public void ShowBalloon(string title, string text, ToolTipIcon icon = ToolTipIcon.Info)
    {
        if (_disposed) return;
        try
        {
            _notify.BalloonTipTitle = title;
            _notify.BalloonTipText = text;
            _notify.BalloonTipIcon = icon;
            _notify.ShowBalloonTip(3500);
        }
        catch { /* ignore */ }
    }

    private void Shell_OnPropertyChanged(object? sender, System.ComponentModel.PropertyChangedEventArgs e)
    {
        if (e.PropertyName is nameof(AppShellViewModel.Connection) or nameof(AppShellViewModel.Server))
            UpdateUi();
    }

    private void UpdateUi()
    {
        if (_disposed) return;
        void Apply()
        {
            if (_disposed) return;
            var state = _shell.Connection.EngineState;
            var label = ConnectionStateMapper.ToStatusLabel(state);
            string tip;
            if (ConnectionStateMapper.IsConnected(state))
            {
                var loc = _shell.Server.Selected?.City;
                if (string.IsNullOrWhiteSpace(loc))
                    loc = _shell.Server.Selected?.DisplayName;
                tip = string.IsNullOrWhiteSpace(loc)
                    ? "Nyxveil — Подключено"
                    : $"Nyxveil — Подключено · {loc}";
            }
            else if (ConnectionStateMapper.Normalize(state) == "Error")
                tip = "Nyxveil — Ошибка подключения";
            else if (ConnectionStateMapper.IsBusy(state))
                tip = label.StartsWith("Отключ", StringComparison.Ordinal)
                    ? $"Nyxveil — {label}"
                    : "Nyxveil — Подключение";
            else
                tip = "Nyxveil — Отключено";

            if (tip.Length > 63)
                tip = tip[..63];
            _notify.Text = tip;

            if (_connectItem is not null)
            {
                var connected = ConnectionStateMapper.IsConnected(state);
                var busy = ConnectionStateMapper.IsBusy(state);
                _connectItem.Text = connected ? "Отключить" : "Подключить";
                // Connecting: do not allow a second Connect; Exit path cancels separately.
                _connectItem.Enabled = connected || !busy;
            }
        }

        var disp = Application.Current?.Dispatcher;
        if (disp is null || disp.CheckAccess())
            Apply();
        else
            _ = disp.BeginInvoke(Apply);
    }

    private ContextMenuStrip BuildMenu()
    {
        var menu = new ContextMenuStrip();
        menu.Items.Add("Открыть Nyxveil", null, (_, _) => _openMain());
        menu.Items.Add(new ToolStripSeparator());
        _connectItem = new ToolStripMenuItem("Подключить", null, async (_, _) =>
        {
            try { await _connectOrDisconnect(); }
            catch { /* surfaced in UI */ }
        });
        menu.Items.Add(_connectItem);
        menu.Items.Add("Серверы", null, (_, _) =>
        {
            _openMain();
            _openServers();
        });
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("Настройки", null, (_, _) =>
        {
            _openMain();
            _openSettings();
        });
        menu.Items.Add("Диагностика", null, (_, _) =>
        {
            _openMain();
            _openDiagnostics();
        });
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("Выход", null, async (_, _) =>
        {
            try { await _exitAsync(); }
            catch { /* ignore */ }
        });
        menu.Opening += (_, _) => UpdateUi();
        return menu;
    }

    private static Icon LoadBrandIcon()
    {
        try
        {
            var exe = Environment.ProcessPath;
            if (!string.IsNullOrEmpty(exe))
            {
                var fromExe = Icon.ExtractAssociatedIcon(exe);
                if (fromExe is not null)
                    return fromExe;
            }
        }
        catch { /* fall through */ }

        var candidates = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "Assets", "Branding", "Windows", "nyxveil.ico"),
            Path.Combine(AppContext.BaseDirectory, "nyxveil.ico"),
        };
        foreach (var c in candidates)
        {
            if (File.Exists(c))
                return new Icon(c);
        }

        return SystemIcons.Application;
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        try { _shell.PropertyChanged -= Shell_OnPropertyChanged; } catch { /* ignore */ }
        _notify.Visible = false;
        _notify.Dispose();
    }
}
