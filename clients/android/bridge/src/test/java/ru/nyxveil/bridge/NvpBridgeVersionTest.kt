package ru.nyxveil.bridge

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * On JVM unit tests the gomobile .so cannot load — nativeAvailable must be false
 * (never hardcoded true). On device/APK the real native path is validated separately.
 */
class NvpBridgeVersionTest {
    @Test
    fun nativeUnavailableOnJvmWithoutSo() {
        // Loading may fail on desktop JVM; either way we must not fake readiness.
        if (NvpBridge.nativeAvailable()) {
            assertTrue(NvpBridge.version().isNotBlank())
            assertEquals("NVP/1", NvpBridge.coreProtocol())
        } else {
            assertFalse(NvpBridge.nativeAvailable())
            assertEquals("NVP/1", NvpBridge.coreProtocol())
        }
    }
}
