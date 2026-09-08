package ru.nyxveil.android.vpn

enum class ConnectionState {
    Disconnected,
    Preparing,
    Connecting,
    Authenticating,
    WaitingForConfig,
    ConfiguringTunnel,
    Connected,
    Reconnecting,
    Disconnecting,
    Error,
    ;

    fun isTransient(): Boolean = when (this) {
        Preparing, Connecting, Authenticating, WaitingForConfig,
        ConfiguringTunnel, Reconnecting, Disconnecting -> true
        else -> false
    }

    fun isBusy(): Boolean = isTransient()
}
