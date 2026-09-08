package ru.nyxveil.android.network

import java.net.Socket

/**
 * Process-wide hook so Control Plane OkHttp sockets can call VpnService.protect
 * while the tunnel is active.
 */
object ControlPlaneNetworking {
    @Volatile
    var protectSocket: ((Socket) -> Boolean)? = null

    fun protectOrPassthrough(socket: Socket): Boolean {
        val p = protectSocket ?: return true
        return p(socket)
    }
}
