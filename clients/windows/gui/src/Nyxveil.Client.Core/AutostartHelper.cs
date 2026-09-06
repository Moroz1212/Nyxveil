using Microsoft.Win32;

namespace Nyxveil.Client.Core;

/// <summary>CurrentUser Run key for launch-with-Windows (no elevation).</summary>
public static class AutostartHelper
{
    private const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "Nyxveil";

    public static void Apply(bool enabled)
    {
        using var key = Registry.CurrentUser.OpenSubKey(RunKey, writable: true)
            ?? Registry.CurrentUser.CreateSubKey(RunKey);
        if (enabled)
        {
            var exe = Environment.ProcessPath;
            if (string.IsNullOrEmpty(exe))
                return;
            key.SetValue(ValueName, "\"" + exe + "\"");
        }
        else
        {
            try { key.DeleteValue(ValueName, throwOnMissingValue: false); }
            catch { /* ignore */ }
        }
    }
}
