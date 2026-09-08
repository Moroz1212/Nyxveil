package ru.nyxveil.android.catalog

import org.json.JSONArray
import org.json.JSONObject
import java.security.KeyFactory
import java.security.Signature
import java.security.spec.X509EncodedKeySpec
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Base64
import java.util.Locale

/**
 * Ed25519 catalog verify matching Control Plane / Go controlplane/catalog.
 * Uses Java 15+ Ed25519 (Temurin 17).
 */
object CatalogVerifier {

    fun parse(signedCatalogJson: ByteArray): SignedCatalogDto {
        val root = JSONObject(String(signedCatalogJson, Charsets.UTF_8))
        val cat = root.getJSONObject("catalog")
        val sigB64 = root.optString("signature", "")
        val signature = if (sigB64.isBlank()) ByteArray(0) else Base64.getDecoder().decode(sigB64)
        return SignedCatalogDto(
            catalog = parseCatalog(cat),
            keyId = root.optString("key_id", ""),
            signature = signature,
        )
    }

    fun verify(
        signed: SignedCatalogDto,
        catalogKeysKidToStdBase64: Map<String, String>,
        nowUtc: Instant = Instant.now(),
    ): CatalogVerifyReport {
        if (signed.keyId.isBlank()) {
            throw CatalogSignatureException("В каталоге отсутствует key_id.")
        }
        if (signed.signature.isEmpty()) {
            throw CatalogSignatureException("В каталоге отсутствует подпись.")
        }
        val b64 = catalogKeysKidToStdBase64[signed.keyId]
            ?: throw CatalogSignatureException("Неизвестный ключ подписи каталога: ${signed.keyId}")
        val pubRaw = try {
            Base64.getDecoder().decode(b64)
        } catch (_: IllegalArgumentException) {
            throw CatalogSignatureException("Некорректный Base64 ключа каталога.")
        }
        if (pubRaw.size != 32) {
            throw CatalogSignatureException("Ключ каталога должен быть 32 байта Ed25519.")
        }

        val payload = CatalogCanonicalJson.buildCanonicalPayload(signed.catalog)
        if (!verifyEd25519(pubRaw, payload, signed.signature)) {
            throw CatalogSignatureException("Подпись каталога недействительна.")
        }

        val issued = CatalogTime.parseInstant(signed.catalog.issuedAt)
        val expires = CatalogTime.parseInstant(signed.catalog.expiresAt)
        val temporalOk = CatalogTime.isTemporallyValid(nowUtc, issued, expires)
        val remaining = expires.epochSecond - nowUtc.epochSecond
        val report = CatalogVerifyReport(
            keyId = signed.keyId,
            issuedAtEpochMs = issued.toEpochMilli(),
            expiresAtEpochMs = expires.toEpochMilli(),
            nowEpochMs = nowUtc.toEpochMilli(),
            remainingSeconds = remaining,
            signature = "PASS",
            temporalValidation = if (temporalOk) "PASS" else "FAIL",
        )
        if (!temporalOk) {
            throw CatalogTemporalException(
                "Срок действия каталога истёк или ещё не начался.",
                report,
            )
        }
        return report
    }

    private fun verifyEd25519(rawPublicKey32: ByteArray, message: ByteArray, signature: ByteArray): Boolean {
        val x509 = encodeEd25519X509(rawPublicKey32)
        val publicKey = KeyFactory.getInstance("Ed25519").generatePublic(X509EncodedKeySpec(x509))
        val sig = Signature.getInstance("Ed25519")
        sig.initVerify(publicKey)
        sig.update(message)
        return sig.verify(signature)
    }

    /** Raw 32-byte Ed25519 public key → SubjectPublicKeyInfo DER. */
    internal fun encodeEd25519X509(raw32: ByteArray): ByteArray {
        require(raw32.size == 32)
        // SEQUENCE { AlgorithmIdentifier { OID 1.3.101.112 }, BIT STRING }
        val oid = byteArrayOf(
            0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70,
        )
        val bitString = ByteArray(2 + 1 + 32)
        bitString[0] = 0x03
        bitString[1] = 33
        bitString[2] = 0x00
        System.arraycopy(raw32, 0, bitString, 3, 32)
        val seqLen = oid.size + bitString.size
        val out = ByteArray(2 + seqLen)
        out[0] = 0x30
        out[1] = seqLen.toByte()
        System.arraycopy(oid, 0, out, 2, oid.size)
        System.arraycopy(bitString, 0, out, 2 + oid.size, bitString.size)
        return out
    }

    private fun parseCatalog(cat: JSONObject): CatalogDto {
        val locations = mutableListOf<LocationDto>()
        val locArr = cat.optJSONArray("locations") ?: JSONArray()
        for (i in 0 until locArr.length()) {
            val o = locArr.getJSONObject(i)
            locations += LocationDto(
                locationId = o.optString("location_id"),
                country = o.optString("country"),
                countryCode = o.optString("country_code"),
                city = o.optString("city"),
                displayName = o.optString("display_name"),
                enabled = o.optBoolean("enabled", false),
            )
        }
        val nodes = mutableListOf<NodeRegistryEntryDto>()
        val nodeArr = cat.optJSONArray("nodes") ?: JSONArray()
        for (i in 0 until nodeArr.length()) {
            val o = nodeArr.getJSONObject(i)
            val endpoints = mutableListOf<EndpointDto>()
            val epArr = o.optJSONArray("endpoints") ?: JSONArray()
            for (j in 0 until epArr.length()) {
                val e = epArr.getJSONObject(j)
                val profiles = mutableListOf<String>()
                val pArr = e.optJSONArray("profiles") ?: JSONArray()
                for (k in 0 until pArr.length()) profiles += pArr.getString(k)
                endpoints += EndpointDto(
                    host = e.optString("host"),
                    port = e.optInt("port"),
                    profiles = profiles,
                    ipFamily = if (e.has("ip_family")) e.optString("ip_family") else null,
                )
            }
            val healthObj = o.optJSONObject("health")
            val health = if (healthObj != null) {
                HealthInfoDto(
                    healthy = healthObj.optBoolean("healthy"),
                    latencyMs = healthObj.optDouble("latency_ms", 0.0),
                    sessionCount = healthObj.optInt("session_count"),
                    cpuPercent = healthObj.optDouble("cpu_percent", 0.0),
                    memoryPercent = healthObj.optDouble("memory_percent", 0.0),
                )
            } else HealthInfoDto()
            val spkiB64 = o.optString("spki_pin", "")
            nodes += NodeRegistryEntryDto(
                nodeId = o.optString("node_id"),
                locationId = o.optString("location_id"),
                country = o.optString("country"),
                city = o.optString("city"),
                displayName = o.optString("display_name"),
                status = o.optString("status"),
                enabled = o.optBoolean("enabled"),
                testOnly = o.optBoolean("test_only"),
                draining = o.optBoolean("draining"),
                protocolVersion = o.optInt("protocol_version"),
                serverVersion = o.optString("server_version"),
                endpoints = endpoints,
                serverName = if (o.has("server_name")) o.optString("server_name") else null,
                spkiPin = if (spkiB64.isBlank()) null else Base64.getDecoder().decode(spkiB64),
                capacity = o.optInt("capacity"),
                currentSessions = o.optInt("current_sessions"),
                health = health,
                lastSeen = o.optString("last_seen"),
            )
        }
        return CatalogDto(
            version = cat.optString("version"),
            locations = locations,
            nodes = nodes,
            issuedAt = cat.optString("issued_at"),
            expiresAt = cat.optString("expires_at"),
        )
    }
}

object CatalogTime {
    /** Matches Windows CatalogTime.MaxIssuedAtSkew / ticket skew — not-before only. */
    val MaxIssuedAtSkew: java.time.Duration = java.time.Duration.ofMinutes(5)

    fun parseInstant(raw: String): Instant {
        if (raw.isBlank()) return Instant.EPOCH
        return try {
            Instant.parse(raw)
        } catch (_: DateTimeParseException) {
            // Unspecified offsets treated as UTC wall via OffsetDateTime → Instant.
            OffsetDateTime.parse(raw).toInstant()
        }
    }

    /**
     * Temporal window (Windows semantics):
     * issued_at − 5m ≤ now ≤ expires_at (inclusive expires; skew only relaxes not-before).
     */
    fun isTemporallyValid(now: Instant, issued: Instant, expires: Instant): Boolean {
        if (now.isAfter(expires)) return false
        if (now.isBefore(issued.minus(MaxIssuedAtSkew))) return false
        return true
    }

    fun formatRfc3339NanoUtc(instant: Instant): String {
        val odt = instant.atOffset(ZoneOffset.UTC)
        var s = odt.format(DateTimeFormatter.ofPattern("yyyy-MM-dd'T'HH:mm:ss.SSSSSSSSS'Z'", Locale.US))
        // Trim trailing zeros in fractional seconds (Go RFC3339Nano style).
        if (s.contains('.')) {
            s = s.replace(Regex("0+Z$"), "Z").replace(".Z", "Z")
        }
        return s
    }
}

/**
 * Canonical catalog JSON matching Go controlplane/catalog.canonicalPayload.
 * Pure string builder (no org.json) so JVM unit tests can verify signatures.
 */
object CatalogCanonicalJson {
    fun buildCanonicalPayload(catalog: CatalogDto): ByteArray {
        val locations = catalog.locations.sortedBy { it.locationId }
        val nodes = catalog.nodes.sortedBy { it.nodeId }
        val sb = StringBuilder()
        sb.append('{')
        sb.append("\"version\":").append(quote(catalog.version)).append(',')
        // Go encoding/json: nil slice → null (Windows matches). Empty production catalogs rare;
        // non-empty lists encode as arrays.
        sb.append("\"locations\":").append(
            if (locations.isEmpty()) "null" else locationsJson(locations),
        ).append(',')
        sb.append("\"nodes\":").append(
            if (nodes.isEmpty()) "null" else nodesJson(nodes),
        ).append(',')
        sb.append("\"issued_at\":").append(
            quote(CatalogTime.formatRfc3339NanoUtc(CatalogTime.parseInstant(catalog.issuedAt))),
        ).append(',')
        sb.append("\"expires_at\":").append(
            quote(CatalogTime.formatRfc3339NanoUtc(CatalogTime.parseInstant(catalog.expiresAt))),
        )
        sb.append('}')
        return sb.toString().toByteArray(Charsets.UTF_8)
    }

    /** Match Go encoding/json float64 (`0` not `0.0`). */
    internal fun formatGoFloat(v: Double): String {
        if (v.isNaN() || v.isInfinite()) {
            throw IllegalArgumentException("catalog float not finite")
        }
        val plain = java.math.BigDecimal.valueOf(v).stripTrailingZeros().toPlainString()
        return if (plain == "-0") "0" else plain
    }

    private fun locationsJson(locations: List<LocationDto>): String {
        val sb = StringBuilder().append('[')
        locations.forEachIndexed { i, l ->
            if (i > 0) sb.append(',')
            sb.append('{')
            sb.append("\"location_id\":").append(quote(l.locationId)).append(',')
            sb.append("\"country\":").append(quote(l.country)).append(',')
            sb.append("\"country_code\":").append(quote(l.countryCode)).append(',')
            sb.append("\"city\":").append(quote(l.city)).append(',')
            sb.append("\"display_name\":").append(quote(l.displayName)).append(',')
            sb.append("\"enabled\":").append(l.enabled)
            sb.append('}')
        }
        return sb.append(']').toString()
    }

    private fun nodesJson(nodes: List<NodeRegistryEntryDto>): String {
        val sb = StringBuilder().append('[')
        nodes.forEachIndexed { i, n ->
            if (i > 0) sb.append(',')
            sb.append('{')
            sb.append("\"node_id\":").append(quote(n.nodeId)).append(',')
            sb.append("\"location_id\":").append(quote(n.locationId)).append(',')
            sb.append("\"country\":").append(quote(n.country)).append(',')
            sb.append("\"city\":").append(quote(n.city)).append(',')
            sb.append("\"display_name\":").append(quote(n.displayName)).append(',')
            sb.append("\"status\":").append(quote(n.status)).append(',')
            sb.append("\"enabled\":").append(n.enabled).append(',')
            sb.append("\"test_only\":").append(n.testOnly).append(',')
            sb.append("\"draining\":").append(n.draining).append(',')
            sb.append("\"protocol_version\":").append(n.protocolVersion).append(',')
            sb.append("\"server_version\":").append(quote(n.serverVersion)).append(',')
            sb.append("\"endpoints\":").append(endpointsJson(n.endpoints)).append(',')
            if (!n.serverName.isNullOrBlank()) {
                sb.append("\"server_name\":").append(quote(n.serverName)).append(',')
            }
            if (n.spkiPin != null) {
                sb.append("\"spki_pin\":").append(quote(Base64.getEncoder().encodeToString(n.spkiPin))).append(',')
            }
            sb.append("\"capacity\":").append(n.capacity).append(',')
            sb.append("\"current_sessions\":").append(n.currentSessions).append(',')
            sb.append("\"health\":{")
            sb.append("\"healthy\":").append(n.health.healthy).append(',')
            sb.append("\"latency_ms\":").append(formatGoFloat(n.health.latencyMs)).append(',')
            sb.append("\"session_count\":").append(n.health.sessionCount).append(',')
            sb.append("\"cpu_percent\":").append(formatGoFloat(n.health.cpuPercent)).append(',')
            sb.append("\"memory_percent\":").append(formatGoFloat(n.health.memoryPercent))
            sb.append("},")
            val lastSeen = if (n.lastSeen.isNotBlank()) {
                CatalogTime.formatRfc3339NanoUtc(CatalogTime.parseInstant(n.lastSeen))
            } else {
                CatalogTime.formatRfc3339NanoUtc(Instant.EPOCH)
            }
            sb.append("\"last_seen\":").append(quote(lastSeen))
            sb.append('}')
        }
        return sb.append(']').toString()
    }

    private fun endpointsJson(endpoints: List<EndpointDto>): String {
        val sb = StringBuilder().append('[')
        endpoints.forEachIndexed { i, e ->
            if (i > 0) sb.append(',')
            sb.append('{')
            sb.append("\"host\":").append(quote(e.host)).append(',')
            sb.append("\"port\":").append(e.port).append(',')
            sb.append("\"profiles\":[")
            e.profiles.forEachIndexed { j, p ->
                if (j > 0) sb.append(',')
                sb.append(quote(p))
            }
            sb.append(']')
            if (e.ipFamily != null) {
                sb.append(',').append("\"ip_family\":").append(quote(e.ipFamily))
            }
            sb.append('}')
        }
        return sb.append(']').toString()
    }

    private fun quote(value: String): String {
        val escaped = value
            .replace("\\", "\\\\")
            .replace("\"", "\\\"")
            .replace("\n", "\\n")
            .replace("\r", "\\r")
            .replace("\t", "\\t")
        return "\"$escaped\""
    }
}
