using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Shapes;

namespace Nyxveil.App.Services;

public static class FlagService
{
    public static FrameworkElement CreateFlag(string? countryCode, double size)
    {
        var code = Normalize(countryCode);
        var root = new Grid { Width = size, Height = size };
        root.Clip = new EllipseGeometry(new Point(size / 2, size / 2), size / 2, size / 2);

        if (code is "FI")
        {
            // Finland: white field, blue Nordic cross (ISO / common depiction).
            root.Children.Add(new Rectangle { Width = size, Height = size, Fill = Brushes.White });
            var blue = new SolidColorBrush(Color.FromRgb(0x00, 0x3A, 0xA5));
            root.Children.Add(new Rectangle
            {
                Width = size * 0.18,
                Height = size,
                Fill = blue,
                HorizontalAlignment = HorizontalAlignment.Left,
                Margin = new Thickness(size * 0.30, 0, 0, 0)
            });
            root.Children.Add(new Rectangle
            {
                Width = size,
                Height = size * 0.18,
                Fill = blue,
                VerticalAlignment = VerticalAlignment.Center
            });
            // Soft rim so white field remains visible on dark glass.
            root.Children.Add(new Ellipse
            {
                Width = size,
                Height = size,
                Stroke = new SolidColorBrush(Color.FromArgb(0x55, 0x2D, 0xEE, 0xD0)),
                StrokeThickness = 1.2,
                Fill = Brushes.Transparent
            });
            return root;
        }

        root.Children.Add(new Ellipse
        {
            Width = size,
            Height = size,
            Fill = new SolidColorBrush(Color.FromRgb(0x14, 0x28, 0x34)),
            Stroke = new SolidColorBrush(Color.FromRgb(0x2D, 0xEE, 0xD0)),
            StrokeThickness = 1
        });
        root.Children.Add(new TextBlock
        {
            Text = string.IsNullOrEmpty(code) ? "?" : code,
            Foreground = Brushes.White,
            FontWeight = FontWeights.SemiBold,
            FontSize = Math.Max(9, size * 0.28),
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center
        });
        return root;
    }

    public static string Normalize(string? countryCode)
    {
        var code = (countryCode ?? "").Trim().ToUpperInvariant();
        if (code is "FIN") return "FI";
        if (code.Length > 2) code = code[..2];
        return code;
    }

    public static string InferFromLocation(string? locationId, string? country, string? countryCode)
    {
        var direct = Normalize(countryCode);
        if (direct.Length == 2) return direct;
        var id = (locationId ?? "").Trim().ToLowerInvariant();
        if (id.StartsWith("fi") || id.Contains("helsinki") || id.Contains("-hel")) return "FI";
        if (id.StartsWith("de") || id.Contains("berlin") || id.Contains("frankfurt")) return "DE";
        if (id.StartsWith("nl") || id.Contains("amsterdam")) return "NL";
        if (id.StartsWith("se") || id.Contains("stockholm")) return "SE";
        if (id.StartsWith("us") || id.Contains("newyork") || id.Contains("nyc")) return "US";
        if (id.StartsWith("gb") || id.StartsWith("uk") || id.Contains("london")) return "GB";
        var c = (country ?? "").Trim().ToLowerInvariant();
        if (c.Contains("финлянд") || c.Contains("finland")) return "FI";
        return "";
    }
}
