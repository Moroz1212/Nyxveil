using System.Text.Json;

namespace Nyxveil.Client.Core;

/// <summary>User-scoped GUI settings (Control Plane URL, preferences).</summary>
public sealed class ClientSettings
{
    private static readonly JsonSerializerOptions JsonOpts = new() { WriteIndented = true };

    public string ControlPlaneBaseUrl { get; set; } = "https://42mou.ru";
    public string? PreferredLocationId { get; set; }
    /// <summary>Launch Nyxveil GUI when Windows starts (CurrentUser Run key).</summary>
    public bool Autostart { get; set; }

    public static string SettingsPath =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Nyxveil", "settings.json");

    public static ClientSettings Load()
    {
        try
        {
            var path = SettingsPath;
            if (!File.Exists(path))
                return new ClientSettings();
            var json = File.ReadAllText(path);
            return JsonSerializer.Deserialize<ClientSettings>(json) ?? new ClientSettings();
        }
        catch
        {
            return new ClientSettings();
        }
    }

    public void Save()
    {
        var dir = Path.GetDirectoryName(SettingsPath)!;
        Directory.CreateDirectory(dir);
        File.WriteAllText(SettingsPath, JsonSerializer.Serialize(this, JsonOpts));
        AutostartHelper.Apply(Autostart);
    }

    public Uri GetBaseUri()
    {
        var s = ControlPlaneBaseUrl.Trim().TrimEnd('/');
        if (s.StartsWith("http://", StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("Control Plane должен использовать HTTPS (SystemTrust).");
        if (!s.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            s = "https://" + s;
        return new Uri(s + "/");
    }

    public string GetControlPlaneHost()
    {
        var uri = GetBaseUri();
        return uri.Host;
    }
}
