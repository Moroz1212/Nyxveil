package ru.nyxveil.android.catalog

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.KeyPairGenerator
import java.security.Signature
import java.time.Instant
import java.util.Base64

class CatalogVerifierTest {
    @Test
    fun temporalFailRejectsExpiredCatalog() {
        val kpg = KeyPairGenerator.getInstance("Ed25519")
        val pair = kpg.generateKeyPair()
        val pubRaw = pair.public.encoded.let { it.copyOfRange(it.size - 32, it.size) }
        val kid = "test-key"

        val issued = Instant.parse("2020-01-01T00:00:00Z")
        val expires = Instant.parse("2020-01-02T00:00:00Z")
        val catalog = CatalogDto(
            version = "1",
            locations = emptyList(),
            nodes = emptyList(),
            issuedAt = issued.toString(),
            expiresAt = expires.toString(),
        )
        val payload = CatalogCanonicalJson.buildCanonicalPayload(catalog)
        val sig = Signature.getInstance("Ed25519")
        sig.initSign(pair.private)
        sig.update(payload)
        val signature = sig.sign()

        val signed = SignedCatalogDto(
            catalog = catalog,
            keyId = kid,
            signature = signature,
        )
        val keys = mapOf(kid to Base64.getEncoder().encodeToString(pubRaw))

        try {
            CatalogVerifier.verify(signed, keys, nowUtc = Instant.parse("2024-06-01T00:00:00Z"))
            throw AssertionError("expected CatalogTemporalException")
        } catch (e: CatalogTemporalException) {
            assertTrue(e.report.temporalValidation == "FAIL")
            assertTrue(e.report.signature == "PASS")
        }
    }

    @Test
    fun temporalSkewAllowsIssuedSlightlyInFuture() {
        val now = Instant.parse("2024-06-01T12:00:00Z")
        val issued = now.plusSeconds(240) // 4 minutes ahead
        val expires = now.plusSeconds(3600)
        assertTrue(CatalogTime.isTemporallyValid(now, issued, expires))
        assertFalse(CatalogTime.isTemporallyValid(now, now.plusSeconds(600), expires))
    }

    @Test
    fun temporalInclusiveExpires() {
        val t = Instant.parse("2024-06-01T12:00:00Z")
        assertTrue(CatalogTime.isTemporallyValid(t, t.minusSeconds(60), t))
        assertFalse(CatalogTime.isTemporallyValid(t.plusSeconds(1), t.minusSeconds(60), t))
    }

    @Test
    fun goFloatFormatMatchesEncodingJson() {
        assertEquals("0", CatalogCanonicalJson.formatGoFloat(0.0))
        assertEquals("1.5", CatalogCanonicalJson.formatGoFloat(1.5))
        assertEquals("12", CatalogCanonicalJson.formatGoFloat(12.0))
    }

    @Test
    fun emptyLocationsNodesCanonicalNull() {
        val catalog = CatalogDto(
            version = "1",
            locations = emptyList(),
            nodes = emptyList(),
            issuedAt = "2024-01-01T00:00:00Z",
            expiresAt = "2024-01-02T00:00:00Z",
        )
        val json = String(CatalogCanonicalJson.buildCanonicalPayload(catalog), Charsets.UTF_8)
        assertTrue(json.contains("\"locations\":null"))
        assertTrue(json.contains("\"nodes\":null"))
    }

    @Test
    fun signatureRoundTripWithHealthFloats() {
        val kpg = KeyPairGenerator.getInstance("Ed25519")
        val pair = kpg.generateKeyPair()
        val pubRaw = pair.public.encoded.let { it.copyOfRange(it.size - 32, it.size) }
        val kid = "k1"
        val now = Instant.parse("2024-06-01T12:00:00Z")
        val catalog = CatalogDto(
            version = "1",
            locations = listOf(
                LocationDto(
                    locationId = "fi-hel",
                    country = "Finland",
                    countryCode = "FI",
                    city = "Helsinki",
                    displayName = "Helsinki",
                    enabled = true,
                ),
            ),
            nodes = listOf(
                NodeRegistryEntryDto(
                    nodeId = "fi-hel-01",
                    locationId = "fi-hel",
                    country = "Finland",
                    city = "Helsinki",
                    displayName = "fi-hel-01",
                    status = "up",
                    enabled = true,
                    health = HealthInfoDto(healthy = true, latencyMs = 0.0, sessionCount = 0),
                    lastSeen = now.toString(),
                ),
            ),
            issuedAt = now.minusSeconds(60).toString(),
            expiresAt = now.plusSeconds(3600).toString(),
        )
        val payload = CatalogCanonicalJson.buildCanonicalPayload(catalog)
        assertTrue(String(payload, Charsets.UTF_8).contains("\"latency_ms\":0"))
        assertFalse(String(payload, Charsets.UTF_8).contains("\"latency_ms\":0.0"))
        val sig = Signature.getInstance("Ed25519")
        sig.initSign(pair.private)
        sig.update(payload)
        val signed = SignedCatalogDto(catalog, kid, sig.sign())
        val report = CatalogVerifier.verify(
            signed,
            mapOf(kid to Base64.getEncoder().encodeToString(pubRaw)),
            nowUtc = now,
        )
        assertEquals("PASS", report.signature)
        assertEquals("PASS", report.temporalValidation)
    }
}
