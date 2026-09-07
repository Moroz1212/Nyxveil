namespace Nyxveil.App.Services;

/// <summary>Exponential moving average of Mbps from byte counter deltas.</summary>
public sealed class SpeedEmaCalculator
{
    private readonly double _alpha;
    private ulong _lastTx;
    private ulong _lastRx;
    private DateTime _lastAt = DateTime.MinValue;
    private bool _seeded;

    public SpeedEmaCalculator(double alpha = 0.35)
    {
        _alpha = alpha;
    }

    public double TxMbps { get; private set; }
    public double RxMbps { get; private set; }

    public void Reset()
    {
        _seeded = false;
        _lastTx = _lastRx = 0;
        TxMbps = RxMbps = 0;
        _lastAt = DateTime.MinValue;
    }

    public void Sample(ulong txBytes, ulong rxBytes, DateTime utcNow)
    {
        if (!_seeded)
        {
            _lastTx = txBytes;
            _lastRx = rxBytes;
            _lastAt = utcNow;
            _seeded = true;
            TxMbps = RxMbps = 0;
            return;
        }

        var dt = (utcNow - _lastAt).TotalSeconds;
        if (dt < 0.2) return;
        if (dt > 5) dt = 5;

        var dTx = txBytes >= _lastTx ? txBytes - _lastTx : 0;
        var dRx = rxBytes >= _lastRx ? rxBytes - _lastRx : 0;
        var instTx = dTx * 8.0 / dt / 1_000_000.0;
        var instRx = dRx * 8.0 / dt / 1_000_000.0;

        TxMbps = _alpha * instTx + (1 - _alpha) * TxMbps;
        RxMbps = _alpha * instRx + (1 - _alpha) * RxMbps;

        _lastTx = txBytes;
        _lastRx = rxBytes;
        _lastAt = utcNow;
    }

    public static string FormatMbps(double mbps, bool connected)
    {
        if (!connected) return "—";
        if (mbps < 0.0005) return "0 бит/с";
        if (mbps < 0.1) return $"{mbps * 1000:0} Кбит/с";
        return $"{mbps:0.0} Мбит/с";
    }
}
