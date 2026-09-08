package ru.nyxveil.android.catalog

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import ru.nyxveil.android.vpn.NyxveilVpnService
import ru.nyxveil.android.vpn.PreparedConnectRequest
import java.time.Instant

class ConnectCatalogBundleTest {

    @Test
    fun rawHashStableForExactBytes() {
        val raw = """{"catalog":{"version":"1"},"key_id":"k","signature":"AA=="}""".toByteArray()
        val a = ConnectCatalogBundle.hash12(raw)
        val b = ConnectCatalogBundle.hash12(raw.copyOf())
        assertEquals(12, a.length)
        assertEquals(a, b)
    }

    @Test
    fun preparedConnectKeepsExactRawBytesNotReserialized() {
        val httpRaw = """{"catalog":{"version":"1","locations":null,"nodes":null,"issued_at":"2024-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z"},"key_id":"k1","signature":"AAAA"}""".toByteArray(Charsets.UTF_8)
        val hash = ConnectCatalogBundle.hash12(httpRaw)
        val prepared = PreparedConnectRequest(
            locationId = "fi-helsinki",
            locationLabel = "Helsinki",
            accessTicket = "ticket",
            rawSignedCatalog = httpRaw.copyOf(),
            catalogKeys = mapOf("k1" to "AAAA"),
            devicePrivateKeySeed32 = ByteArray(32),
            catalogRawHash12 = hash,
            catalogSource = "network",
        )
        assertArrayEquals(httpRaw, prepared.rawSignedCatalog)
        assertEquals(hash, ConnectCatalogBundle.hash12(prepared.rawSignedCatalog))
        // Re-encoding DTO would change whitespace / nulls — prove we never go that path.
        val reserialized = String(httpRaw).replace("null", "[]").toByteArray()
        assertFalse(httpRaw.contentEquals(reserialized))
        assertNotEquals(hash, ConnectCatalogBundle.hash12(reserialized))
    }

    @Test
    fun expiredCacheMustNotBeUsedWhenNowPastExpires() {
        val now = Instant.parse("2026-06-01T12:00:00Z")
        val issued = Instant.parse("2026-05-01T00:00:00Z")
        val expires = Instant.parse("2026-05-02T00:00:00Z")
        assertFalse(CatalogTime.isTemporallyValid(now, issued, expires))
    }

    @Test
    fun userFacingMapsCatalogExpiredNotServerUnavailable() {
        val msg = NyxveilVpnService.userFacingConnectError(
            Exception("begin: catalog verify: catalog expired"),
        )
        assertEquals("Не удалось обновить данные серверов.", msg)
        assertFalse(msg.contains("Сервер недоступен"))
    }

    @Test
    fun userFacingMapsSignature() {
        val msg = NyxveilVpnService.userFacingConnectError(
            CatalogSignatureException("Не удалось проверить данные серверов."),
        )
        assertEquals("Не удалось проверить данные серверов.", msg)
    }

    @Test
    fun temporalSkewStillWindowsSemanticsForKotlinUi() {
        val now = Instant.parse("2024-06-01T12:00:00Z")
        assertTrue(CatalogTime.isTemporallyValid(now, now.plusSeconds(240), now.plusSeconds(3600)))
    }
}
