package ru.nyxveil.android.ui.home

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import ru.nyxveil.android.vpn.NyxveilVpnService

class HomeUiLabelsTest {
    @Test
    fun transportLabelSeparateFromProtocol() {
        assertEquals("QUIC UDP / 443", formatTransport("quic-udp-443"))
        assertEquals("TLS", formatTransport("tls-tcp-443"))
    }

    @Test
    fun catalogExpiredNotServerUnavailable() {
        val msg = NyxveilVpnService.userFacingConnectError(Exception("tun_read: closed"))
        // tun errors map to tunnel message
        assertTrue(msg.contains("туннел") || msg.contains("VPN"))
        assertFalse(
            NyxveilVpnService.userFacingConnectError(Exception("begin: catalog verify: catalog expired"))
                .contains("Сервер недоступен"),
        )
    }
}
