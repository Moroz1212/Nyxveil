package ru.nyxveil.android.network

import java.net.InetAddress
import java.net.Socket
import javax.net.SocketFactory

/**
 * OkHttp socket factory that applies [VpnService.protect] before connect so Control Plane
 * traffic stays on the physical network while the VPN is up.
 */
class ProtectingSocketFactory(
    private val protect: (Socket) -> Boolean,
) : SocketFactory() {
    override fun createSocket(): Socket = ProtectedSocket(protect)

    override fun createSocket(host: String?, port: Int): Socket {
        val s = createSocket()
        s.connect(java.net.InetSocketAddress(host, port))
        return s
    }

    override fun createSocket(host: String?, port: Int, localHost: InetAddress?, localPort: Int): Socket {
        val s = createSocket()
        s.bind(java.net.InetSocketAddress(localHost, localPort))
        s.connect(java.net.InetSocketAddress(host, port))
        return s
    }

    override fun createSocket(host: InetAddress?, port: Int): Socket {
        val s = createSocket()
        s.connect(java.net.InetSocketAddress(host, port))
        return s
    }

    override fun createSocket(
        address: InetAddress?,
        port: Int,
        localAddress: InetAddress?,
        localPort: Int,
    ): Socket {
        val s = createSocket()
        s.bind(java.net.InetSocketAddress(localAddress, localPort))
        s.connect(java.net.InetSocketAddress(address, port))
        return s
    }

    private class ProtectedSocket(
        private val protect: (Socket) -> Boolean,
    ) : Socket() {
        @Volatile
        private var protected = false

        private fun ensureProtected() {
            if (protected) return
            if (!protect(this)) {
                throw java.net.SocketException("VpnService.protect failed for Control Plane socket")
            }
            protected = true
        }

        override fun connect(endpoint: java.net.SocketAddress?) {
            ensureProtected()
            super.connect(endpoint)
        }

        override fun connect(endpoint: java.net.SocketAddress?, timeout: Int) {
            ensureProtected()
            super.connect(endpoint, timeout)
        }
    }
}
