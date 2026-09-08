package ru.nyxveil.android.vpn

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import ru.nyxveil.android.logging.AppLog
import ru.nyxveil.android.logging.LogCategory
import ru.nyxveil.bridge.NvpBridge
import java.util.Base64

interface VpnSessionEngine {
    suspend fun begin(request: BeginParams): TypeConfigResult
    suspend fun attachTun(tunFd: Int): EngineStats
    suspend fun disconnect()
    fun currentStats(): EngineStats

    data class BeginParams(
        val locationId: String,
        val locationLabel: String,
        val accessTicket: String,
        val catalogJson: ByteArray,
        val catalogKeys: Map<String, String>,
        val devicePrivateKeySeed32: ByteArray,
        val protect: (Int) -> Boolean,
    )

    data class TypeConfigResult(
        val vpnIp: String,
        val vpnPrefix: Int,
        val dnsServers: List<String>,
        val typeConfigMtu: Int,
        val effectiveTunnelMtu: Int,
        val maxDatagramPayload: Long,
        val nvpOverhead: Int,
        val transport: String,
        val nodeId: String,
        val rawJson: String,
    )

    data class EngineStats(
        val state: ConnectionState,
        val vpnIp: String = "",
        val pingMs: Int? = null,
        val downloadMbps: Double? = null,
        val uploadMbps: Double? = null,
        val dns: String = "",
        val mtu: Int = 0,
        val transport: String = "",
        val protocol: String = "NVP/1",
        val message: String = "",
        val txBytes: Long = 0,
        val rxBytes: Long = 0,
        val txDatagramTooLarge: Long = 0,
        val tunReadPackets: Long = 0,
        val nvpTxPackets: Long = 0,
        val nvpRxPackets: Long = 0,
        val tunWritePackets: Long = 0,
        val trafficIdle: Boolean = true,
        val txPumpAlive: Boolean = false,
    )
}

class DefaultVpnSessionEngine : VpnSessionEngine {
    @Volatile
    private var stats = VpnSessionEngine.EngineStats(state = ConnectionState.Disconnected)

    @Volatile
    private var native: NvpBridge.NativeEngine? = null

    private var lastTx: Long = 0
    private var lastRx: Long = 0
    private var lastSampleAt: Long = 0
    private var emaDown: Double? = null
    private var emaUp: Double? = null
    private var loggedFirstTunRead = false
    private var loggedFirstNvpTx = false
    private var loggedFirstNvpRx = false
    private var loggedFirstTunWrite = false

    override suspend fun begin(request: VpnSessionEngine.BeginParams): VpnSessionEngine.TypeConfigResult =
        withContext(Dispatchers.IO) {
            if (!NvpBridge.nativeAvailable()) {
                val why = NvpBridge.loadError() ?: "native unavailable"
                throw IllegalStateException("Нативный NVP движок недоступен ($why).")
            }
            val engine = NvpBridge.createEngine()
                ?: throw IllegalStateException("Мост NVP недоступен.")
            native?.disconnect()
            native = engine
            engine.setProtector(object : NvpBridge.ProtectCallback {
                override fun protect(fd: Int): Boolean = request.protect(fd)
            })
            stats = stats.copy(state = ConnectionState.Connecting, message = "")
            val keysJson = JSONObject().apply {
                request.catalogKeys.forEach { (k, v) -> put(k, v) }
            }.toString()
            val rawHash = ru.nyxveil.android.catalog.ConnectCatalogBundle.hash12(request.catalogJson)
            val config = JSONObject()
                .put("location_id", request.locationId)
                .put("access_ticket", request.accessTicket)
                .put(
                    "device_private_key_b64",
                    Base64.getEncoder().encodeToString(request.devicePrivateKeySeed32),
                )
                .put(
                    "catalog_json_b64",
                    Base64.getEncoder().encodeToString(request.catalogJson),
                )
                .put("catalog_keys_json", keysJson)
                .toString()
            AppLog.i(LogCategory.TRANSPORT, "native BeginJSON raw_hash=$rawHash")
            val typeJson = engine.beginJSON(config)
            val parsed = parseTypeConfig(typeJson)
            stats = stats.copy(
                state = ConnectionState.WaitingForConfig,
                vpnIp = parsed.vpnIp,
                dns = parsed.dnsServers.joinToString(", "),
                mtu = parsed.effectiveTunnelMtu,
                transport = parsed.transport.ifBlank { NvpBridge.coreProtocol() },
            )
            parsed
        }

    override suspend fun attachTun(tunFd: Int): VpnSessionEngine.EngineStats =
        withContext(Dispatchers.IO) {
            val engine = native ?: throw IllegalStateException("Сначала Begin.")
            AppLog.i(LogCategory.TUN, "native AttachTun")
            engine.attachTun(tunFd)
            // New session: reset speed EMA baseline so prior session deltas cannot spike UI.
            lastTx = 0
            lastRx = 0
            lastSampleAt = 0
            emaDown = null
            emaUp = null
            loggedFirstTunRead = false
            loggedFirstNvpTx = false
            loggedFirstNvpRx = false
            loggedFirstTunWrite = false
            refreshFromStatus()
            // Prime baseline without publishing a synthetic Mbps sample.
            lastTx = stats.txBytes
            lastRx = stats.rxBytes
            lastSampleAt = System.currentTimeMillis()
            if (stats.state != ConnectionState.Connected) {
                throw IllegalStateException(stats.message.ifBlank { "Dataplane не готов." })
            }
            stats
        }

    override suspend fun disconnect() = withContext(Dispatchers.IO) {
        try {
            native?.disconnect()
        } catch (_: Exception) {
        }
        native = null
        lastTx = 0
        lastRx = 0
        lastSampleAt = 0
        emaDown = null
        emaUp = null
        loggedFirstTunRead = false
        loggedFirstNvpTx = false
        loggedFirstNvpRx = false
        loggedFirstTunWrite = false
        stats = VpnSessionEngine.EngineStats(state = ConnectionState.Disconnected)
    }

    override fun currentStats(): VpnSessionEngine.EngineStats {
        refreshFromStatus()
        return stats
    }

    private fun refreshFromStatus() {
        val engine = native ?: return
        try {
            val json = JSONObject(engine.statusJSON())
            val connected = json.optBoolean("connected", false)
            val tx = json.optLong("tx_bytes", 0)
            val rx = json.optLong("rx_bytes", 0)
            val msg = json.optString("message")
            val txAlive = json.optBoolean("tx_pump_alive", false)
            val now = System.currentTimeMillis()
            var down: Double? = null
            var up: Double? = null
            if (connected && lastSampleAt > 0) {
                val dt = (now - lastSampleAt).coerceAtLeast(1) / 1000.0
                val dDown = ((rx - lastRx).coerceAtLeast(0) * 8.0 / dt / 1_000_000.0)
                val dUp = ((tx - lastTx).coerceAtLeast(0) * 8.0 / dt / 1_000_000.0)
                emaDown = ema(emaDown, dDown)
                emaUp = ema(emaUp, dUp)
                down = emaDown
                up = emaUp
            }
            lastTx = tx
            lastRx = rx
            lastSampleAt = now

            if (json.optBoolean("first_tun_read") && !loggedFirstTunRead) {
                loggedFirstTunRead = true
                AppLog.i(LogCategory.DATAPLANE, "tun_read first_packet packets=${json.optLong("tun_read_packets")}")
            }
            if (json.optBoolean("first_nvp_tx") && !loggedFirstNvpTx) {
                loggedFirstNvpTx = true
                AppLog.i(LogCategory.DATAPLANE, "nvp_tx first_packet packets=${json.optLong("nvp_tx_packets")}")
            }
            if (json.optBoolean("first_nvp_rx") && !loggedFirstNvpRx) {
                loggedFirstNvpRx = true
                AppLog.i(LogCategory.DATAPLANE, "nvp_rx first_packet packets=${json.optLong("nvp_rx_packets")}")
            }
            if (json.optBoolean("first_tun_write") && !loggedFirstTunWrite) {
                loggedFirstTunWrite = true
                AppLog.i(LogCategory.DATAPLANE, "tun_write first_packet packets=${json.optLong("tun_write_packets")}")
            }

            val prev = stats.state
            val nextState = when {
                connected -> ConnectionState.Connected
                msg.isNotBlank() && (
                    prev == ConnectionState.Connected ||
                        prev == ConnectionState.ConfiguringTunnel ||
                        prev == ConnectionState.WaitingForConfig
                    ) -> ConnectionState.Error
                msg.contains("tun_read") || msg.contains("nvp_tx") -> ConnectionState.Error
                else -> prev
            }
            stats = VpnSessionEngine.EngineStats(
                state = nextState,
                vpnIp = json.optString("vpn_ip"),
                pingMs = null,
                downloadMbps = down,
                uploadMbps = up,
                dns = json.optString("dns"),
                mtu = json.optInt("mtu", json.optInt("effective_tunnel_mtu", 0)),
                transport = json.optString("transport"),
                protocol = json.optString("protocol").ifBlank { "NVP/1" },
                message = msg,
                txBytes = tx,
                rxBytes = rx,
                txDatagramTooLarge = json.optLong("tx_datagram_too_large", 0),
                tunReadPackets = json.optLong("tun_read_packets", 0),
                nvpTxPackets = json.optLong("nvp_tx_packets", 0),
                nvpRxPackets = json.optLong("nvp_rx_packets", 0),
                tunWritePackets = json.optLong("tun_write_packets", 0),
                trafficIdle = json.optBoolean("traffic_idle", true),
                txPumpAlive = txAlive,
            )
        } catch (_: Exception) {
        }
    }

    private fun ema(prev: Double?, sample: Double): Double {
        val a = 0.35
        return if (prev == null) sample else prev * (1 - a) + sample * a
    }

    companion object {
        fun parseTypeConfig(typeJson: String): VpnSessionEngine.TypeConfigResult {
            val json = JSONObject(typeJson)
            val dnsArr = json.optJSONArray("dns_servers") ?: JSONArray()
            val dns = buildList {
                for (i in 0 until dnsArr.length()) {
                    val s = dnsArr.optString(i)
                    if (s.isNotBlank()) add(s)
                }
            }
            val vpnIp = json.getString("vpn_ip")
            val prefix = json.getInt("vpn_prefix")
            val eff = json.getInt("effective_tunnel_mtu")
            require(vpnIp.isNotBlank()) { "TypeConfig: vpn_ip missing" }
            require(dns.isNotEmpty()) { "TypeConfig: dns_servers required" }
            require(eff > 0) { "TypeConfig: effective_tunnel_mtu missing" }
            return VpnSessionEngine.TypeConfigResult(
                vpnIp = vpnIp,
                vpnPrefix = prefix,
                dnsServers = dns,
                typeConfigMtu = json.optInt("typeconfig_mtu", 0),
                effectiveTunnelMtu = eff,
                maxDatagramPayload = json.optLong("max_datagram_payload", 0),
                nvpOverhead = json.optInt("nvp_overhead", 0),
                transport = json.optString("transport"),
                nodeId = json.optString("node_id"),
                rawJson = typeJson,
            )
        }
    }
}
