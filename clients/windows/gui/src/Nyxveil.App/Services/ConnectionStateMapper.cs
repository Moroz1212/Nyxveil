namespace Nyxveil.App.Services;

public static class ConnectionStateMapper
{
    public static string Normalize(string? state)
    {
        var s = (state ?? "").Trim();
        if (s.Length == 0) return "Disconnected";

        // Canonical PascalCase used by UI.
        return s.ToLowerInvariant() switch
        {
            "connected" => "Connected",
            "disconnected" or "idle" => "Disconnected",
            "authenticating" => "Authenticating",
            "waitingforconfig" or "waiting_for_config" => "WaitingForConfig",
            "configuringtunnel" or "configuring_tunnel" => "ConfiguringTunnel",
            "disconnecting" => "Disconnecting",
            "reconnecting" => "Reconnecting",
            "error" => "Error",
            "connecting" or "connectingtransport" or "connecting_transport"
                or "preparing" or "validating" => "ConnectingTransport",
            _ when s.Contains("auth", StringComparison.OrdinalIgnoreCase) => "Authenticating",
            _ when s.Contains("wait", StringComparison.OrdinalIgnoreCase) &&
                   s.Contains("config", StringComparison.OrdinalIgnoreCase) => "WaitingForConfig",
            _ when s.Contains("tunnel", StringComparison.OrdinalIgnoreCase) ||
                   s.Contains("configur", StringComparison.OrdinalIgnoreCase) => "ConfiguringTunnel",
            _ when s.Contains("reconnect", StringComparison.OrdinalIgnoreCase) => "Reconnecting",
            _ when s.Contains("disconnect", StringComparison.OrdinalIgnoreCase) => "Disconnecting",
            _ when s.Contains("error", StringComparison.OrdinalIgnoreCase) ||
                   s.Contains("fail", StringComparison.OrdinalIgnoreCase) => "Error",
            _ when s.Contains("connect", StringComparison.OrdinalIgnoreCase) => "ConnectingTransport",
            _ => s
        };
    }

    public static string ToStatusLabel(string? state) => Normalize(state) switch
    {
        "Connected" => "Подключено",
        "Disconnected" => "Отключено",
        "Authenticating" => "Подключение",
        "WaitingForConfig" => "Подключение",
        "ConfiguringTunnel" => "Подключение",
        "Disconnecting" => "Отключение",
        "Reconnecting" => "Переподключение",
        "Error" => "Ошибка",
        "ConnectingTransport" or "Preparing" or "Validating" => "Подключение",
        _ => "Подключение"
    };

    public static string ToRingVisual(string? state) => Normalize(state) switch
    {
        "Connected" => "Connected",
        "Error" => "Error",
        "Disconnected" => "Disconnected",
        "Disconnecting" => "Disconnecting",
        _ => "Connecting"
    };

    public static bool IsConnected(string? state) =>
        Normalize(state) == "Connected";

    public static bool IsBusy(string? state) =>
        Normalize(state) is "ConnectingTransport" or "Preparing" or "Validating" or "Authenticating"
            or "WaitingForConfig" or "ConfiguringTunnel" or "Disconnecting" or "Reconnecting";
}
