using System.Threading;

namespace Nyxveil.App;

/// <summary>
/// Named mutex + activation/quit events so a second launch restores or quits the existing GUI.
/// </summary>
internal sealed class SingleInstanceGuard : IDisposable
{
    public const string MutexName = @"Local\Nyxveil.Gui.SingleInstance.v1";
    public const string ActivateEventName = @"Local\Nyxveil.Gui.Activate.v1";
    public const string QuitEventName = @"Local\Nyxveil.Gui.Quit.v1";

    private readonly Mutex _mutex;
    private readonly EventWaitHandle _activate;
    private readonly EventWaitHandle _quit;
    private readonly bool _ownsMutex;
    private Thread? _watcher;
    private volatile bool _stop;

    private SingleInstanceGuard(Mutex mutex, bool ownsMutex, EventWaitHandle activate, EventWaitHandle quit)
    {
        _mutex = mutex;
        _ownsMutex = ownsMutex;
        _activate = activate;
        _quit = quit;
    }

    public static bool TryEnter(out SingleInstanceGuard? guard)
    {
        var mutex = new Mutex(initiallyOwned: true, MutexName, out var createdNew);
        var activate = new EventWaitHandle(false, EventResetMode.AutoReset, ActivateEventName);
        var quit = new EventWaitHandle(false, EventResetMode.AutoReset, QuitEventName);
        if (!createdNew)
        {
            try { activate.Set(); } catch { /* ignore */ }
            activate.Dispose();
            quit.Dispose();
            try { mutex.ReleaseMutex(); } catch { /* ignore */ }
            mutex.Dispose();
            guard = null;
            return false;
        }

        guard = new SingleInstanceGuard(mutex, ownsMutex: true, activate, quit);
        return true;
    }

    /// <summary>Signals the running GUI to exit (installer uninstall / --quit).</summary>
    public static void SignalQuit()
    {
        try
        {
            using var quit = EventWaitHandle.OpenExisting(QuitEventName);
            quit.Set();
        }
        catch
        {
            try
            {
                using var quit = new EventWaitHandle(false, EventResetMode.AutoReset, QuitEventName);
                quit.Set();
            }
            catch { /* ignore */ }
        }
    }

    public void StartWatching(Action onActivate, Action onQuit)
    {
        _watcher = new Thread(() =>
        {
            var handles = new WaitHandle[] { _activate, _quit };
            while (!_stop)
            {
                try
                {
                    var idx = WaitHandle.WaitAny(handles, 500);
                    if (_stop) break;
                    if (idx == 0)
                        onActivate();
                    else if (idx == 1)
                        onQuit();
                }
                catch
                {
                    break;
                }
            }
        })
        {
            IsBackground = true,
            Name = "Nyxveil.SingleInstance.Activate"
        };
        _watcher.Start();
    }

    public void Dispose()
    {
        _stop = true;
        try { _activate.Set(); } catch { /* ignore */ }
        try { _quit.Set(); } catch { /* ignore */ }
        try { _watcher?.Join(1000); } catch { /* ignore */ }
        _activate.Dispose();
        _quit.Dispose();
        if (_ownsMutex)
        {
            try { _mutex.ReleaseMutex(); } catch { /* ignore */ }
        }
        _mutex.Dispose();
    }
}
