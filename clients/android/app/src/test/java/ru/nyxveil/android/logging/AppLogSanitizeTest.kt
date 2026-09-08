package ru.nyxveil.android.logging

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AppLogSanitizeTest {
    @Test
    fun redactsSecretsAndUrls() {
        val raw = "Authorization: Bearer super-secret-token https://cp.nyxveil.ru:18443/api license_token=abc access_ticket=xyz"
        val clean = AppLog.sanitize(raw)
        assertFalse(clean.contains("super-secret"))
        assertFalse(clean.contains("cp.nyxveil"))
        assertFalse(clean.contains("abc"))
        assertTrue(clean.contains("[redacted]") || clean.contains("Bearer [redacted]"))
    }
}
