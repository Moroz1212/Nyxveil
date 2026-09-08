package ru.nyxveil.android.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class DataplaneErrorMappingTest {
    @Test
    fun tunReadFatalMapsToTunnelMessage() {
        val msg = NyxveilVpnService.userFacingConnectError(Exception("tun_read: EBADF"))
        assertTrue(msg.contains("туннел") || msg.lowercase().contains("tun"))
    }

    @Test
    fun protocolConstantIsNvp1() {
        assertEquals("NVP/1", VpnSessionEngine.EngineStats(state = ConnectionState.Connected).protocol)
    }
}
