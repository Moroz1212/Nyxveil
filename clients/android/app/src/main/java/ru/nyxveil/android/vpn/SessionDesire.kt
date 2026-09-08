package ru.nyxveil.android.vpn

/**
 * Gates path-change reconnect vs explicit user Disconnect / VPN revoke.
 * Pure logic for soak + unit tests (no Android deps).
 */
class SessionDesire {
    @Volatile
    var userDesiredConnected: Boolean = false
        private set

    @Volatile
    var reconnectInProgress: Boolean = false
        private set

    fun onUserConnectRequested() {
        userDesiredConnected = true
    }

    fun onUserDisconnectRequested() {
        userDesiredConnected = false
        reconnectInProgress = false
    }

    fun beginReconnect(): Boolean {
        if (!userDesiredConnected) return false
        if (reconnectInProgress) return false
        reconnectInProgress = true
        return true
    }

    fun endReconnect() {
        reconnectInProgress = false
    }

    /** Service reached Disconnected (user disconnect, revoke, or crash). */
    fun onObservedDisconnected() {
        if (!reconnectInProgress) {
            userDesiredConnected = false
        }
    }

    fun shouldReconnectOnPathEvent(
        connectionState: ConnectionState,
    ): Boolean {
        if (!userDesiredConnected) return false
        if (reconnectInProgress) return false
        return when (connectionState) {
            ConnectionState.Connected,
            ConnectionState.Error,
            -> true
            else -> false
        }
    }
}
