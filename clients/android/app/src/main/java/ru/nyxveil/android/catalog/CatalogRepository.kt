package ru.nyxveil.android.catalog

import android.content.Context
import org.json.JSONObject
import ru.nyxveil.android.licensing.ControlPlaneClient
import ru.nyxveil.android.logging.AppLog
import ru.nyxveil.android.logging.LogCategory
import ru.nyxveil.bridge.NvpBridge
import java.io.File
import java.time.Instant
import java.util.Base64
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Catalog repository.
 *
 * Browse ([fetchAndVerify]): network preferred; valid cache fallback.
 * Connect ([prepareConnectCatalog]): mandatory fresh network when possible; cache only if
 * **Frozen Core** verify still PASSes right now (no Windows skew override).
 *
 * Native Begin must receive [ConnectCatalogBundle.rawSignedCatalog] unchanged.
 */
class CatalogRepository(
    private val context: Context,
    private val cp: ControlPlaneClient,
) {
    private val cacheFile: File
        get() = File(context.filesDir, "catalog-cache.json")

    private val refreshInFlight = AtomicBoolean(false)

    @Volatile
    var lastDiag: CatalogDiag = CatalogDiag()
        private set

    /** Last Core-verified connect bundle (immutable). */
    @Volatile
    var lastConnectBundle: ConnectCatalogBundle? = null
        private set

    data class Loaded(
        val signed: SignedCatalogDto,
        val rawJson: ByteArray,
        val keys: CatalogKeys,
        val fromCache: Boolean,
        val report: CatalogVerifyReport,
        val rawHash12: String,
        val coreVerified: Boolean,
    )

    fun tryBeginRefresh(): Boolean = refreshInFlight.compareAndSet(false, true)

    fun endRefresh() {
        refreshInFlight.set(false)
    }

    suspend fun fetchAndVerify(licenseToken: String): Loaded {
        lastDiag = CatalogDiag(state = CatalogState.Loading, userSummary = "загрузка", httpStage = "start")
        val cached = tryLoadCache()
        try {
            return fetchNetworkAndCache(licenseToken, forConnect = false)
        } catch (fetchEx: Exception) {
            when (fetchEx) {
                is CatalogTemporalException, is CatalogSignatureException -> throw fetchEx
                else -> {
                    if (cached != null) {
                        CatalogLog.stage("cache", "TRY_FALLBACK")
                        return verifyLoaded(cached.raw, cached.keys, fromCache = true, forConnect = false)
                    }
                    throw fetchEx
                }
            }
        }
    }

    /**
     * Fresh catalog for Connect: network first, Core-strict verify, immutable bundle.
     * Cache only if Core still accepts it now.
     */
    suspend fun prepareConnectCatalog(licenseToken: String): ConnectCatalogBundle {
        AppLog.i(LogCategory.CATALOG, "connect refresh start")
        CatalogLog.stage("connect", "REFRESH_BEGIN")
        val loaded = try {
            fetchNetworkAndCache(licenseToken, forConnect = true)
        } catch (fetchEx: Exception) {
            if (fetchEx is CatalogTemporalException || fetchEx is CatalogSignatureException) throw fetchEx
            val cached = tryLoadCache() ?: throw fetchEx
            CatalogLog.stage("connect", "NETWORK_FAIL_TRY_CACHE")
            try {
                verifyLoaded(cached.raw, cached.keys, fromCache = true, forConnect = true)
            } catch (e: CatalogTemporalException) {
                CatalogLog.fail("connect", "CACHE_EXPIRED", e)
                throw fetchEx
            } catch (e: CatalogSignatureException) {
                clearCorruptCache()
                throw fetchEx
            }
        }
        val issued = CatalogTime.parseInstant(loaded.signed.catalog.issuedAt)
        val expires = CatalogTime.parseInstant(loaded.signed.catalog.expiresAt)
        val now = Instant.now()
        require(loaded.coreVerified) { "connect catalog must be Frozen Core verified" }
        val bundle = ConnectCatalogBundle(
            rawSignedCatalog = loaded.rawJson.copyOf(),
            keys = loaded.keys,
            signed = loaded.signed,
            source = if (loaded.fromCache) "cache" else "network",
            rawHash12 = loaded.rawHash12,
            issuedAt = issued,
            expiresAt = expires,
            verifiedAt = now,
            remainingSeconds = remainingSeconds(now, expires),
        )
        lastConnectBundle = bundle
        AppLog.i(
            LogCategory.CATALOG,
            "source=${bundle.source} raw_hash=${bundle.rawHash12} key_id=${bundle.signed.keyId} " +
                "issued_at=${bundle.issuedAt} expires_at=${bundle.expiresAt} now_utc=$now " +
                "remaining_seconds=${bundle.remainingSeconds} native_verify=PASS",
        )
        return bundle
    }

    fun locationsGroupedByCity(catalog: CatalogDto): List<LocationUi> {
        return catalog.locations
            .filter { it.enabled }
            .sortedWith(compareBy({ it.city.lowercase() }, { it.displayName.lowercase() }))
            .map {
                LocationUi(
                    locationId = it.locationId,
                    displayName = it.displayName.ifBlank { "${it.city}, ${it.country}" },
                    city = it.city,
                    country = it.country,
                )
            }
    }

    fun clearCorruptCache() {
        try {
            if (cacheFile.exists()) cacheFile.delete()
            CatalogLog.stage("cache", "DELETED_CORRUPT")
        } catch (_: Exception) {
        }
    }

    private suspend fun fetchNetworkAndCache(licenseToken: String, forConnect: Boolean): Loaded {
        lastDiag = lastDiag.copy(httpStage = "keys", state = CatalogState.Loading)
        CatalogLog.stage("keys", "BEGIN")
        val keys = try {
            val k = cp.getCatalogKeys(licenseToken)
            CatalogLog.stage("keys", "HTTP_OK", "key_count=${k.keys.size}")
            if (k.keys.isEmpty()) {
                CatalogLog.fail("keys", "EMPTY")
                lastDiag = CatalogDiag(
                    state = CatalogState.KeysError,
                    userSummary = catalogUserMessage(CatalogState.KeysError),
                    httpStage = "keys",
                    detail = "empty keys map",
                )
                throw ControlPlaneClient.ControlPlaneException(
                    "Не удалось загрузить список локаций.\nПроверьте подключение к интернету и повторите попытку.",
                )
            }
            k
        } catch (e: ControlPlaneClient.ControlPlaneException) {
            CatalogLog.fail("keys", "HTTP_FAIL", e)
            lastDiag = CatalogDiag(
                state = CatalogState.HttpError,
                userSummary = e.message ?: catalogUserMessage(CatalogState.HttpError),
                httpStage = "keys",
                detail = e.javaClass.simpleName,
            )
            throw e
        } catch (e: Exception) {
            CatalogLog.fail("keys", "NETWORK_FAIL", e)
            lastDiag = CatalogDiag(
                state = CatalogState.NetworkError,
                userSummary = catalogUserMessage(CatalogState.NetworkError),
                httpStage = "keys",
                detail = e.javaClass.simpleName,
            )
            throw e
        }

        lastDiag = lastDiag.copy(httpStage = "catalog")
        CatalogLog.stage("catalog", "BEGIN")
        val raw = try {
            val body = cp.getCatalogRaw(licenseToken)
            val hash = ConnectCatalogBundle.hash12(body)
            CatalogLog.stage("catalog", "HTTP_OK", "bytes=${body.size} raw_hash=$hash")
            AppLog.i(LogCategory.CATALOG, "network raw_hash=$hash bytes=${body.size}")
            body
        } catch (e: ControlPlaneClient.ControlPlaneException) {
            CatalogLog.fail("catalog", "HTTP_FAIL", e)
            lastDiag = CatalogDiag(
                state = CatalogState.HttpError,
                userSummary = e.message ?: catalogUserMessage(CatalogState.HttpError),
                httpStage = "catalog",
                detail = e.javaClass.simpleName,
            )
            throw e
        } catch (e: Exception) {
            CatalogLog.fail("catalog", "NETWORK_FAIL", e)
            lastDiag = CatalogDiag(
                state = CatalogState.NetworkError,
                userSummary = catalogUserMessage(CatalogState.NetworkError),
                httpStage = "catalog",
                detail = e.javaClass.simpleName,
            )
            throw e
        }

        val loaded = verifyLoaded(raw, keys, fromCache = false, forConnect = forConnect)
        saveCache(raw, keys)
        return loaded
    }

    /**
     * @param forConnect when true (or native available), Frozen Core verify is fail-closed —
     * no Windows skew override of Core "catalog expired".
     */
    private fun verifyLoaded(
        raw: ByteArray,
        keys: CatalogKeys,
        fromCache: Boolean,
        forConnect: Boolean,
    ): Loaded {
        val rawHash = ConnectCatalogBundle.hash12(raw)
        CatalogLog.stage("parse", "BEGIN", "raw_hash=$rawHash")
        val signed = try {
            CatalogVerifier.parse(raw)
        } catch (e: Exception) {
            CatalogLog.fail("parse", "FAIL", e)
            if (fromCache) clearCorruptCache()
            lastDiag = CatalogDiag(
                state = CatalogState.ParseError,
                userSummary = catalogUserMessage(CatalogState.ParseError),
                httpStage = if (fromCache) "cache" else "catalog",
                source = if (fromCache) "cache" else "network",
                detail = e.javaClass.simpleName,
            )
            throw CatalogSignatureException("Не удалось проверить данные серверов.")
        }
        CatalogLog.stage("parse", "PASS", "key_id=${signed.keyId} raw_hash=$rawHash")

        val now = Instant.now()
        val issued = CatalogTime.parseInstant(signed.catalog.issuedAt)
        val expires = CatalogTime.parseInstant(signed.catalog.expiresAt)
        AppLog.i(
            LogCategory.CATALOG,
            "verify raw_hash=$rawHash key_id=${signed.keyId} issued_at=$issued expires_at=$expires " +
                "now_utc=$now remaining_seconds=${remainingSeconds(now, expires)}",
        )

        if (NvpBridge.nativeAvailable()) {
            CatalogLog.stage("signature", "BEGIN", "via=frozen_core key_id=${signed.keyId}")
            try {
                NvpBridge.catalogVerify(raw, keys.keys)
            } catch (e: Exception) {
                val msg = (e.message ?: "").lowercase()
                CatalogLog.fail("signature", "FAIL", e, "key_id=${signed.keyId} raw_hash=$rawHash")
                AppLog.e(
                    LogCategory.CATALOG,
                    "native_verify=FAIL raw_hash=$rawHash issued_at=$issued expires_at=$expires now_utc=$now",
                    e,
                )
                if (fromCache) clearCorruptCache()
                if (msg.contains("expired") || msg.contains("catalog expired")) {
                    val report = CatalogVerifyReport(
                        keyId = signed.keyId,
                        issuedAtEpochMs = issued.toEpochMilli(),
                        expiresAtEpochMs = expires.toEpochMilli(),
                        nowEpochMs = now.toEpochMilli(),
                        remainingSeconds = remainingSeconds(now, expires),
                        signature = "PASS",
                        temporalValidation = "FAIL",
                    )
                    lastDiag = diagFrom(CatalogState.TemporalError, signed, report, fromCache, "temporal")
                    throw CatalogTemporalException(
                        "Не удалось обновить данные серверов.",
                        report,
                    )
                }
                lastDiag = CatalogDiag(
                    state = CatalogState.SignatureError,
                    userSummary = catalogUserMessage(CatalogState.SignatureError),
                    source = if (fromCache) "cache" else "network",
                    keyId = signed.keyId.ifBlank { "—" },
                    signatureStatus = "FAIL",
                    temporalStatus = "—",
                    httpStage = if (fromCache) "cache" else "verify",
                    detail = e.javaClass.simpleName,
                )
                throw CatalogSignatureException("Не удалось проверить данные серверов.")
            }
            CatalogLog.stage("signature", "PASS", "key_id=${signed.keyId}")
            CatalogLog.stage("temporal", "PASS", "remaining_s=${remainingSeconds(now, expires)} via=frozen_core")
            AppLog.i(LogCategory.CATALOG, "native_verify=PASS raw_hash=$rawHash")
            val report = CatalogVerifyReport(
                keyId = signed.keyId,
                issuedAtEpochMs = issued.toEpochMilli(),
                expiresAtEpochMs = expires.toEpochMilli(),
                nowEpochMs = now.toEpochMilli(),
                remainingSeconds = remainingSeconds(now, expires),
                signature = "PASS",
                temporalValidation = "PASS",
            )
            lastDiag = diagFrom(CatalogState.Ready, signed, report, fromCache, "ready")
            return Loaded(signed, raw, keys, fromCache, report, rawHash, coreVerified = true)
        }

        // JVM unit tests without native: Kotlin verifier (includes Windows issued_at skew).
        CatalogLog.stage("signature", "BEGIN", "via=kotlin key_id=${signed.keyId}")
        val report = CatalogVerifier.verify(signed, keys.keys, now)
        CatalogLog.stage("signature", "PASS", "key_id=${signed.keyId}")
        CatalogLog.stage("temporal", "PASS", "remaining_s=${report.remainingSeconds}")
        lastDiag = diagFrom(CatalogState.Ready, signed, report, fromCache, "ready")
        // Connect on device always has native; mark coreVerified false for JVM-only path.
        if (forConnect) {
            throw IllegalStateException("Connect requires native Frozen Core catalog verify.")
        }
        return Loaded(signed, raw, keys, fromCache, report, rawHash, coreVerified = false)
    }

    private fun diagFrom(
        state: CatalogState,
        signed: SignedCatalogDto,
        report: CatalogVerifyReport,
        fromCache: Boolean,
        httpStage: String,
    ): CatalogDiag {
        return CatalogDiag(
            state = state,
            userSummary = when (state) {
                CatalogState.Ready ->
                    buildString {
                        append(if (fromCache) "кэш" else "сеть")
                        append(" · готов")
                    }
                CatalogState.TemporalError -> "Не удалось обновить данные серверов."
                else -> catalogUserMessage(state)
            },
            source = if (fromCache) "cache" else "network",
            keyId = report.keyId.ifBlank { signed.keyId }.ifBlank { "—" },
            issuedAt = Instant.ofEpochMilli(report.issuedAtEpochMs).asDiagString(),
            expiresAt = Instant.ofEpochMilli(report.expiresAtEpochMs).asDiagString(),
            remainingSeconds = report.remainingSeconds,
            signatureStatus = report.signature,
            temporalStatus = report.temporalValidation,
            httpStage = httpStage,
        )
    }

    private data class CacheEntry(val raw: ByteArray, val keys: CatalogKeys)

    private fun tryLoadCache(): CacheEntry? {
        if (!cacheFile.exists()) return null
        return try {
            val root = JSONObject(cacheFile.readText())
            val rawB64 = root.optString("signed_catalog_json_b64", "")
            if (rawB64.isBlank()) return null
            val raw = Base64.getDecoder().decode(rawB64)
            val keysObj = root.getJSONObject("keys")
            val map = mutableMapOf<String, String>()
            val keysJson = keysObj.optJSONObject("keys") ?: JSONObject()
            val it = keysJson.keys()
            while (it.hasNext()) {
                val k = it.next()
                map[k] = keysJson.getString(k)
            }
            val keys = CatalogKeys(
                issuer = keysObj.optString("issuer"),
                keys = map,
                updatedAt = keysObj.optLong("updated_at"),
            )
            CacheEntry(raw, keys)
        } catch (e: Exception) {
            CatalogLog.fail("cache", "CORRUPT", e)
            clearCorruptCache()
            null
        }
    }

    private fun saveCache(raw: ByteArray, keys: CatalogKeys) {
        val keysMap = JSONObject()
        keys.keys.forEach { (k, v) -> keysMap.put(k, v) }
        val keysObj = JSONObject()
            .put("issuer", keys.issuer)
            .put("keys", keysMap)
            .put("updated_at", keys.updatedAt)
        val root = JSONObject()
            .put("signed_catalog_json_b64", Base64.getEncoder().encodeToString(raw))
            .put("keys", keysObj)
            .put("saved_at_utc", Instant.now().toString())
            .put("raw_hash_12", ConnectCatalogBundle.hash12(raw))
        val tmp = File(cacheFile.parentFile, cacheFile.name + ".tmp")
        tmp.writeText(root.toString())
        if (!tmp.renameTo(cacheFile)) {
            cacheFile.writeText(tmp.readText())
            tmp.delete()
        }
        CatalogLog.stage("cache", "SAVED", "raw_hash=${ConnectCatalogBundle.hash12(raw)}")
    }
}
