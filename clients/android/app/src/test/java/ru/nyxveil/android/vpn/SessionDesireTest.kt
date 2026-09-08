package ru.nyxveil.android.vpn

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SessionDesireTest {
    @Test
    fun userDisconnectBlocksReconnect() {
        val d = SessionDesire()
        d.onUserConnectRequested()
        assertTrue(d.shouldReconnectOnPathEvent(ConnectionState.Connected))
        d.onUserDisconnectRequested()
        assertFalse(d.shouldReconnectOnPathEvent(ConnectionState.Connected))
        assertFalse(d.beginReconnect())
    }

    @Test
    fun revokeClearsDesireWhenNotReconnecting() {
        val d = SessionDesire()
        d.onUserConnectRequested()
        d.onObservedDisconnected()
        assertFalse(d.userDesiredConnected)
        assertFalse(d.shouldReconnectOnPathEvent(ConnectionState.Disconnected))
    }

    @Test
    fun reconnectKeepsDesireAcrossIntermediateDisconnect() {
        val d = SessionDesire()
        d.onUserConnectRequested()
        assertTrue(d.beginReconnect())
        d.onObservedDisconnected()
        assertTrue(d.userDesiredConnected)
        d.endReconnect()
        assertTrue(d.userDesiredConnected)
    }

    @Test
    fun soakConnectDisconnectDesire() {
        val d = SessionDesire()
        repeat(50) {
            d.onUserConnectRequested()
            assertTrue(d.userDesiredConnected)
            d.onUserDisconnectRequested()
            assertFalse(d.userDesiredConnected)
            d.onObservedDisconnected()
            assertFalse(d.beginReconnect())
        }
    }
}
