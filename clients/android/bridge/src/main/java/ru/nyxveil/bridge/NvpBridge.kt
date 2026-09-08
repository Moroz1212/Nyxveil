package ru.nyxveil.bridge

import nyxveilbridge.Engine
import nyxveilbridge.Nyxveilbridge
import nyxveilbridge.Protector

/**
 * Kotlin façade over the gomobile AAR (`nyxveilbridge`).
 *
 * [nativeAvailable] is true only when the native library loads and [Nyxveilbridge.nativeReady]
 * returns true — never hardcoded.
 */
object NvpBridge {
    @Volatile
    private var loadError: String? = null

    @Volatile
    private var ready: Boolean = false

    init {
        try {
            // gomobile loads libgojni via Seq; touching the class triggers load.
            val v = Nyxveilbridge.version()
            ready = Nyxveilbridge.nativeReady() && v.isNotBlank()
            if (!ready) {
                loadError = "nativeReady=false"
            }
        } catch (t: Throwable) {
            ready = false
            loadError = t.javaClass.simpleName + ": " + (t.message ?: "load failed")
        }
    }

    fun version(): String = try {
        if (ready) Nyxveilbridge.version() else "unavailable"
    } catch (_: Throwable) {
        "unavailable"
    }

    fun coreProtocol(): String = try {
        if (ready) Nyxveilbridge.protocol() else "NVP/1"
    } catch (_: Throwable) {
        "NVP/1"
    }

    fun nativeAvailable(): Boolean = ready

    fun loadError(): String? = loadError

    /**
     * Verifies signed catalog bytes using Frozen Core (gomobile).
     * [keysKidToStdBase64] is key_id → standard Base64 Ed25519 public key.
     */
    fun catalogVerify(signedCatalogJson: ByteArray, keysKidToStdBase64: Map<String, String>) {
        if (!ready) {
            throw IllegalStateException("native catalog verify unavailable")
        }
        val keysJson = org.json.JSONObject().apply {
            keysKidToStdBase64.forEach { (k, v) -> put(k, v) }
        }.toString().toByteArray(Charsets.UTF_8)
        Nyxveilbridge.catalogVerify(signedCatalogJson, keysJson)
    }

    fun createEngine(): NativeEngine? {
        if (!ready) return null
        return try {
            NativeEngine(Nyxveilbridge.newEngine())
        } catch (t: Throwable) {
            loadError = t.message
            null
        }
    }

    fun setProtectCallback(callback: ProtectCallback?) {
        protectCallback = callback
    }

    @Volatile
    var protectCallback: ProtectCallback? = null
        private set

    fun interface ProtectCallback {
        fun protect(fd: Int): Boolean
    }

    /** Thin wrapper around gomobile [Engine]. */
    class NativeEngine internal constructor(private val engine: Engine) {
        fun setProtector(cb: ProtectCallback) {
            engine.setProtector(Protector { fd -> cb.protect(fd.toInt()) })
        }

        /** Phase 1: dial + AUTH + TypeConfig JSON. */
        fun beginJSON(configJSON: String): String = engine.beginJSON(configJSON)

        /** Phase 2: own TUN fd and start dataplane. */
        fun attachTun(fd: Int) {
            engine.attachTun(fd.toLong())
        }

        fun disconnect() {
            engine.disconnect()
        }

        fun statusJSON(): String = engine.statusJSON()
    }
}
