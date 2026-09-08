package ru.nyxveil.android.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PhysicalNetworkTrackerTest {
    private var now = 1_000L
    private val tracker = PhysicalNetworkTracker(debounceMs = 100L, nowMs = { now })

    @Test
    fun vpnNetworksIgnored() {
        assertNull(
            tracker.onAvailable("vpn1", PhysicalNetworkTracker.Transport.OTHER, isVpn = true),
        )
        assertNull(
            tracker.onCapabilitiesChanged("vpn1", PhysicalNetworkTracker.Transport.OTHER, isVpn = true),
        )
        assertNull(tracker.onLost("vpn1", isVpn = true))
    }

    @Test
    fun capabilityRefreshSameNetworkNoEvent() {
        val first = tracker.onAvailable("n1", PhysicalNetworkTracker.Transport.WIFI, false)
        assertEquals(PhysicalNetworkTracker.EventKind.CHANGED, first!!.kind)
        now += 200
        assertNull(
            tracker.onCapabilitiesChanged("n1", PhysicalNetworkTracker.Transport.WIFI, false),
        )
        // Burst of capability updates
        repeat(20) {
            now += 10
            assertNull(
                tracker.onCapabilitiesChanged("n1", PhysicalNetworkTracker.Transport.WIFI, false),
            )
        }
    }

    @Test
    fun wifiAppearWhileCellularDoesNotSwitch() {
        now += 200
        tracker.onAvailable("cell", PhysicalNetworkTracker.Transport.CELLULAR, false)
        now += 200
        assertNull(
            tracker.onAvailable("wifi", PhysicalNetworkTracker.Transport.WIFI, false),
        )
        assertEquals("cell", tracker.activeIdentity()?.networkKey)
    }

    @Test
    fun wifiLostFallsBackToCellular() {
        now += 200
        tracker.onAvailable("wifi", PhysicalNetworkTracker.Transport.WIFI, false)
        now += 200
        tracker.onAvailable("cell", PhysicalNetworkTracker.Transport.CELLULAR, false)
        now += 200
        val ev = tracker.onLost("wifi", false)
        assertEquals(PhysicalNetworkTracker.EventKind.CHANGED, ev!!.kind)
        assertEquals("cell", ev.to?.networkKey)
        assertTrue(ev.label.contains("→"))
    }

    @Test
    fun allPhysicalLostEmitsLost() {
        now += 200
        tracker.onAvailable("wifi", PhysicalNetworkTracker.Transport.WIFI, false)
        now += 200
        val ev = tracker.onLost("wifi", false)
        assertEquals(PhysicalNetworkTracker.EventKind.LOST, ev!!.kind)
        assertEquals("lost", ev.label)
    }

    @Test
    fun duplicateBurstDeduped() {
        now += 200
        val a = tracker.onAvailable("wifi", PhysicalNetworkTracker.Transport.WIFI, false)
        assertTrue(a != null)
        // Immediate re-available same key within dedupe window
        assertNull(tracker.onAvailable("wifi", PhysicalNetworkTracker.Transport.WIFI, false))
    }
}
