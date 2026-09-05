using System.Globalization;
using System.Text;

namespace Nyxveil.ControlPlane.Web.Metrics;

public sealed record ChartDot(double X, double Y, DateTime Time, double Value);
public sealed record ChartGeometry(string Path, IReadOnlyList<ChartDot> Dots, double Maximum)
{
    public static string Svg(double value) => value.ToString("0.###", CultureInfo.InvariantCulture);
    public static ChartGeometry Create(IReadOnlyList<MetricPoint> points, Func<MetricPoint, double?> value,
        DateTime from, DateTime until, double? fixedMaximum = null)
    {
        var values = points.Select(value).Where(v => v.HasValue && double.IsFinite(v.Value) && v >= 0)
            .Select(v => v!.Value).ToList();
        var max = fixedMaximum ?? Math.Max(1, values.DefaultIfEmpty(0).Max() * 1.1);
        var path = new StringBuilder();
        var dots = new List<ChartDot>();
        var connected = false;
        foreach (var point in points)
        {
            var v = value(point);
            if (v is null || !double.IsFinite(v.Value) || v < 0 || v > max || point.Time < from || point.Time > until)
            {
                connected = false;
                continue;
            }
            var x = 52 + (point.Time - from).TotalSeconds / Math.Max(1, (until - from).TotalSeconds) * 564;
            var y = 190 - v.Value / max * 164;
            path.Append(connected ? " L " : " M ").Append(Svg(x)).Append(' ').Append(Svg(y));
            dots.Add(new ChartDot(x, y, point.Time, v.Value));
            connected = true;
        }
        return new ChartGeometry(path.ToString(), dots, max);
    }
}
