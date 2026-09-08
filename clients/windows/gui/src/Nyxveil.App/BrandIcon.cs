using System.IO;
using System.Windows;
using System.Windows.Media.Imaging;

namespace Nyxveil.App;

/// <summary>Applies the approved Nyxveil Windows icon to WPF windows.</summary>
internal static class BrandIcon
{
    private static BitmapFrame? _cached;

    public static void ApplyTo(Window window)
    {
        try
        {
            _cached ??= Load();
            if (_cached is not null)
                window.Icon = _cached;
        }
        catch { /* non-fatal */ }
    }

    private static BitmapFrame? Load()
    {
        var candidates = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "Assets", "Branding", "Windows", "nyxveil.ico"),
            Path.Combine(AppContext.BaseDirectory, "nyxveil.ico"),
        };
        foreach (var path in candidates)
        {
            if (!File.Exists(path))
                continue;
            return BitmapFrame.Create(new Uri(path, UriKind.Absolute), BitmapCreateOptions.None, BitmapCacheOption.OnLoad);
        }

        try
        {
            var exe = Environment.ProcessPath;
            if (!string.IsNullOrEmpty(exe) && File.Exists(exe))
                return BitmapFrame.Create(new Uri(exe, UriKind.Absolute), BitmapCreateOptions.None, BitmapCacheOption.OnLoad);
        }
        catch { /* ignore */ }

        return null;
    }
}
