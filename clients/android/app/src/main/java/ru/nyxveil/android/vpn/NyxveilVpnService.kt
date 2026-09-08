package ru.nyxveil.android.vpn

import android.app.Notification
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Binder
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.system.OsConstants
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import ru.nyxveil.android.MainActivity
import ru.nyxveil.android.R
import ru.nyxveil.android.catalog.CatalogSignatureException
import ru.nyxveil.android.catalog.CatalogTemporalException
import ru.nyxveil.android.logging.AppLog
import ru.nyxveil.android.logging.LogCategory
import ru.nyxveil.android.network.ControlPlaneNetworking
import ru.nyxveil.bridge.NvpBridge
import java.net.InetAddress
import java.net.Socket

/**
 * VPN service. Connect order:
 * Begin (protected transport + AUTH + TypeConfig) → Builder → establish → detachFd → AttachTun → Connected.
 *
 * PREPARING ROOT CAUSE (fixed): wrong FGS type (non-vpn) + mutex held across native Begin +
 * UI stuck in Preparing during ticket without stage updates.
 *
 * TUN FD: [ParcelFileDescriptor.detachFd] → Go owns and closes on Disconnect.
 */
class NyxveilVpnService : VpnService() {
    inner class LocalBinder : Binder() {
        val service: NyxveilVpnService get() = this@NyxveilVpnService
    }

    data class SessionSnapshot(
        val state: ConnectionState,
        val locationLabel: String,
        val vpnIp: String,
        val dns: String,
        val mtu: Int,
        val typeConfigMtu: Int,
        val maxDatagram: Long,
        val transport: String,
        val protocol: String,
        val pingMs: Int?,
        val downloadMbps: Double?,
        val uploadMbps: Double?,
        val message: String,
        val connectedAtMs: Long,
        val txBytes: Long,
        val rxBytes: Long,
        val tunReadPackets: Long = 0,
        val nvpTxPackets: Long = 0,
        val nvpRxPackets: Long = 0,
        val tunWritePackets: Long = 0,
        val trafficIdle: Boolean = true,
        val txPumpAlive: Boolean = false,
    )

    private val binder = LocalBinder()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val sessionMutex = Mutex()
    private val engine: VpnSessionEngine = DefaultVpnSessionEngine()

    val stateFlow = MutableStateFlow(ConnectionState.Disconnected)
    private var stateListener: ((ConnectionState) -> Unit)? = null

    @Volatile
    private var desiredConnected: Boolean = false

    private var tunPfd: ParcelFileDescriptor? = null
    private var tunOwnedByNative: Boolean = false

    private var sessionJob: Job? = null
    private var locationLabel: String = ""
    private var vpnIp: String = ""
    private var dnsJoined: String = ""
    private var mtuValue: Int = 0
    private var typeConfigMtu: Int = 0
    private var maxDatagram: Long = 0
    private var transport: String = ""
    private var lastMessage: String = ""
    private var connectedAtMs: Long = 0

    override fun onBind(intent: Intent?): IBinder = binder

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_DISCONNECT) {
            scope.launch { disconnectSession() }
            return START_NOT_STICKY
        }
        return START_STICKY
    }

    fun setStateListener(listener: (ConnectionState) -> Unit) {
        stateListener = listener
        listener(stateFlow.value)
    }

    fun snapshot(): SessionSnapshot {
        val stats = engine.currentStats()
        return SessionSnapshot(
            state = stateFlow.value,
            locationLabel = locationLabel,
            vpnIp = vpnIp.ifBlank { stats.vpnIp },
            dns = dnsJoined.ifBlank { stats.dns },
            mtu = if (mtuValue > 0) mtuValue else stats.mtu,
            typeConfigMtu = typeConfigMtu,
            maxDatagram = maxDatagram,
            transport = transport.ifBlank { stats.transport },
            protocol = stats.protocol.ifBlank { "NVP/1" },
            pingMs = stats.pingMs,
            downloadMbps = stats.downloadMbps,
            uploadMbps = stats.uploadMbps,
            message = lastMessage.ifBlank { stats.message },
            connectedAtMs = connectedAtMs,
            txBytes = stats.txBytes,
            rxBytes = stats.rxBytes,
            tunReadPackets = stats.tunReadPackets,
            nvpTxPackets = stats.nvpTxPackets,
            nvpRxPackets = stats.nvpRxPackets,
            tunWritePackets = stats.tunWritePackets,
            trafficIdle = stats.trafficIdle,
            txPumpAlive = stats.txPumpAlive,
        )
    }

    fun protectFd(fd: Int): Boolean {
        val ok = protect(fd)
        AppLog.i(LogCategory.TRANSPORT, "protect(fd) result=${if (ok) "PASS" else "FAIL"}")
        return ok
    }

    /**
     * Starts connect asynchronously. Does not hold [sessionMutex] across native Begin
     * (so Disconnect can cancel). Caller may await [sessionJob] via [awaitSession].
     */
    suspend fun connectSession(request: PreparedConnectRequest) {
        sessionMutex.withLock {
            if (!NvpBridge.nativeAvailable()) {
                val why = NvpBridge.loadError() ?: "unknown"
                throw IllegalStateException("Нативный NVP движок недоступен ($why).")
            }
            if (sessionJob?.isActive == true) {
                AppLog.w(LogCategory.VPN, "Connect ignored — session already in progress")
                return
            }
            desiredConnected = true
            this.locationLabel = request.locationLabel
            ControlPlaneNetworking.protectSocket = { s: Socket -> protect(s) }
            setState(ConnectionState.Preparing)
            startVpnForeground(buildNotification(request.locationLabel, connecting = true))
            AppLog.i(
                LogCategory.VPN,
                "Connect session start location=${request.locationId} raw_hash=${request.catalogRawHash12} source=${request.catalogSource}",
            )

            sessionJob = scope.launch {
                try {
                    runConnectPipeline(request)
                } catch (e: CancellationException) {
                    AppLog.i(LogCategory.VPN, "Connect cancelled")
                    throw e
                } catch (e: Exception) {
                    AppLog.e(LogCategory.ERROR, e.message ?: "connect failed", e)
                    lastMessage = userFacingConnectError(e)
                    setState(ConnectionState.Error)
                    ControlPlaneNetworking.protectSocket = null
                    try {
                        engine.disconnect()
                    } catch (_: Exception) {
                    }
                    cleanupTunLocalOnly()
                    tunOwnedByNative = false
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
            }
        }
        // Join outside mutex so Disconnect can acquire lock and cancel.
        try {
            sessionJob?.join()
        } catch (_: CancellationException) {
        }
    }

    private suspend fun runConnectPipeline(request: PreparedConnectRequest) {
        if (!desiredConnected) return
        // Catalog already Core-verified in ViewModel; stay Preparing until dial starts.
        AppLog.i(
            LogCategory.VPN,
            "Begin raw_hash=${request.catalogRawHash12} location=${request.locationId}",
        )
        setState(ConnectionState.Connecting)
        AppLog.i(LogCategory.TRANSPORT, "node selection / Dial begin")

        val typeCfg = withTimeout(CONNECT_TIMEOUT_MS) {
            engine.begin(
                VpnSessionEngine.BeginParams(
                    locationId = request.locationId,
                    locationLabel = request.locationLabel,
                    accessTicket = request.accessTicket,
                    catalogJson = request.rawSignedCatalog,
                    catalogKeys = request.catalogKeys,
                    devicePrivateKeySeed32 = request.devicePrivateKeySeed32,
                    protect = { protectFd(it) },
                ),
            )
        }
        if (!desiredConnected) {
            engine.disconnect()
            return
        }

        AppLog.i(
            LogCategory.AUTH,
            "AUTH_OK / session established transport=${typeCfg.transport.ifBlank { "nvp" }}",
        )
        AppLog.i(
            LogCategory.TYPECONFIG,
            "VPN IP=${typeCfg.vpnIp} prefix=${typeCfg.vpnPrefix} typeconfig_mtu=${typeCfg.typeConfigMtu}",
        )
        AppLog.i(
            LogCategory.MTU,
            "max_datagram=${typeCfg.maxDatagramPayload} nvp_overhead=${typeCfg.nvpOverhead} effective_mtu=${typeCfg.effectiveTunnelMtu}",
        )
        AppLog.i(LogCategory.DNS, "servers=${typeCfg.dnsServers.joinToString(",")}")

        setState(ConnectionState.WaitingForConfig)
        setState(ConnectionState.ConfiguringTunnel)
        establishTun(
            hostAddress = typeCfg.vpnIp,
            prefixLength = typeCfg.vpnPrefix,
            dns = typeCfg.dnsServers,
            mtu = typeCfg.effectiveTunnelMtu,
        )
        vpnIp = typeCfg.vpnIp
        dnsJoined = typeCfg.dnsServers.joinToString(", ")
        mtuValue = typeCfg.effectiveTunnelMtu
        typeConfigMtu = typeCfg.typeConfigMtu
        maxDatagram = typeCfg.maxDatagramPayload
        transport = typeCfg.transport
        AppLog.i(LogCategory.TUN, "Android TUN established mtu=$mtuValue")

        if (!desiredConnected) {
            cleanupTunLocalOnly()
            engine.disconnect()
            return
        }

        val pfd = tunPfd ?: throw IllegalStateException("TUN не создан.")
        val fd = pfd.detachFd()
        tunPfd = null
        tunOwnedByNative = true
        AppLog.i(LogCategory.TUN, "FD detached — ownership transferred to native")
        try {
            withTimeout(ATTACH_TIMEOUT_MS) {
                engine.attachTun(fd)
            }
        } catch (e: Exception) {
            tunOwnedByNative = false
            throw e
        }

        if (!desiredConnected) {
            engine.disconnect()
            tunOwnedByNative = false
            return
        }
        lastMessage = ""
        connectedAtMs = System.currentTimeMillis()
        setState(ConnectionState.Connected)
        startVpnForeground(buildNotification(locationLabel, connecting = false))
        AppLog.i(LogCategory.DATAPLANE, "TX/RX started Connected tun_blocking=true")
        // Watch for fatal dataplane pump death while UI shows Connected.
        scope.launch {
            while (desiredConnected && stateFlow.value == ConnectionState.Connected) {
                kotlinx.coroutines.delay(1000)
                val st = engine.currentStats()
                if (st.state == ConnectionState.Error || (st.message.isNotBlank() && !st.txPumpAlive)) {
                    lastMessage = userFacingConnectError(Exception(st.message.ifBlank { "dataplane failed" }))
                    AppLog.e(LogCategory.DATAPLANE, "fatal pump message=${st.message}")
                    setState(ConnectionState.Error)
                    break
                }
            }
        }
    }

    suspend fun disconnectSession() = sessionMutex.withLock {
        AppLog.i(LogCategory.VPN, "Disconnect requested")
        desiredConnected = false
        ControlPlaneNetworking.protectSocket = null
        setState(ConnectionState.Disconnecting)
        sessionJob?.cancel()
        sessionJob = null
        try {
            engine.disconnect()
        } catch (_: Exception) {
        }
        cleanupTunLocalOnly()
        tunOwnedByNative = false
        connectedAtMs = 0
        setState(ConnectionState.Disconnected)
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        AppLog.i(LogCategory.VPN, "Disconnected")
    }

    override fun onRevoke() {
        AppLog.w(LogCategory.VPN, "VpnService.onRevoke — clearing session")
        desiredConnected = false
        ControlPlaneNetworking.protectSocket = null
        scope.launch {
            try {
                engine.disconnect()
            } catch (_: Exception) {
            }
            cleanupTunLocalOnly()
            tunOwnedByNative = false
            connectedAtMs = 0
            lastMessage = "Разрешение VPN отозвано."
            setState(ConnectionState.Disconnected)
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    override fun onDestroy() {
        desiredConnected = false
        scope.cancel()
        cleanupTunLocalOnly()
        super.onDestroy()
    }

    private fun establishTun(hostAddress: String, prefixLength: Int, dns: List<String>, mtu: Int) {
        cleanupTunLocalOnly()
        val hostIp = InetAddress.getByName(hostAddress)
        require(!isNetworkBaseAddress(hostIp, prefixLength)) {
            "Адрес VPN не должен быть базой сети."
        }
        val builder = Builder()
            .setSession("Nyxveil")
            .setMtu(mtu)
            .addAddress(hostAddress, prefixLength)
            .addRoute("0.0.0.0", 0)
        // IPv4-only TypeConfig: claim IPv6 default routes so traffic is not leaked on physical IPv6
        // (packets hit TUN and are dropped by IPv4-only dataplane — fail-closed vs bypass).
        try {
            builder.addRoute("::", 0)
        } catch (_: Exception) {
            AppLog.w(LogCategory.TUN, "IPv6 default route not accepted by Builder — IPv6 may bypass VPN")
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            try {
                builder.allowFamily(OsConstants.AF_INET)
                builder.allowFamily(OsConstants.AF_INET6)
            } catch (_: Exception) {
            }
        }
        dns.forEach { builder.addDnsServer(it) }
        // Android VpnService FD is non-blocking by default; Go dataplane uses blocking Read.
        builder.setBlocking(true)
        AppLog.i(LogCategory.TUN, "VpnService.Builder setBlocking(true) routes=0.0.0.0/0 dns=${dns.joinToString(",")}")
        tunPfd = builder.establish()
            ?: throw IllegalStateException("Система отклонила создание VPN-интерфейса.")
        tunOwnedByNative = false
    }

    private fun cleanupTunLocalOnly() {
        try {
            tunPfd?.close()
        } catch (_: Exception) {
        }
        tunPfd = null
    }

    private fun setState(state: ConnectionState) {
        stateFlow.value = state
        stateListener?.invoke(state)
        AppLog.i(LogCategory.VPN, "state=${state.name}")
    }

    private fun startVpnForeground(notification: Notification) {
        // API 34+: VpnService qualifies as systemExempted (FOREGROUND_SERVICE_TYPE_VPN removed in SDK 35).
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            ServiceCompat.startForeground(
                this,
                NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED,
            )
        } else {
            @Suppress("DEPRECATION")
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun buildNotification(loc: String, connecting: Boolean): Notification {
        val open = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val disconnectPi = PendingIntent.getService(
            this,
            1,
            Intent(this, NyxveilVpnService::class.java).setAction(ACTION_DISCONNECT),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val title = if (connecting) {
            getString(R.string.vpn_connecting_title)
        } else {
            getString(R.string.vpn_connected_title)
        }
        val text = when {
            connecting && loc.isNotBlank() -> "Подключение · $loc"
            connecting -> "Подключение..."
            loc.isNotBlank() -> "Подключено · $loc"
            else -> "Подключено"
        }
        return NotificationCompat.Builder(this, getString(R.string.vpn_notification_channel))
            .setContentTitle(title)
            .setContentText(text)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(open)
            .setOngoing(true)
            .addAction(0, getString(R.string.vpn_disconnect), disconnectPi)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .build()
    }

    companion object {
        const val ACTION_DISCONNECT = "ru.nyxveil.android.vpn.DISCONNECT"
        private const val NOTIFICATION_ID = 18443
        private const val CONNECT_TIMEOUT_MS = 90_000L
        private const val ATTACH_TIMEOUT_MS = 15_000L

        fun isNetworkBaseAddress(address: InetAddress, prefixLength: Int): Boolean {
            val bytes = address.address
            if (bytes.size != 4) return false
            if (prefixLength !in 0..32) return false
            val ip = ((bytes[0].toInt() and 0xff) shl 24) or
                ((bytes[1].toInt() and 0xff) shl 16) or
                ((bytes[2].toInt() and 0xff) shl 8) or
                (bytes[3].toInt() and 0xff)
            val mask = if (prefixLength == 0) 0 else -1 shl (32 - prefixLength)
            return (ip and mask.inv()) == 0 && prefixLength < 32
        }

        fun userFacingConnectError(e: Exception): String {
            val m = (e.message ?: "").lowercase()
            return when {
                e is CatalogTemporalException ||
                    m.contains("catalog expired") ||
                    m.contains("catalog verify") && m.contains("expired") ||
                    m.contains("обновить данные серверов") ->
                    "Не удалось обновить данные серверов."
                e is CatalogSignatureException ||
                    (m.contains("catalog") && (m.contains("signature") || m.contains("invalid"))) ||
                    m.contains("проверить данные серверов") ->
                    "Не удалось проверить данные серверов."
                m.contains("no healthy") || m.contains("nohealthy") || m.contains("нет доступных") ->
                    "Нет доступных серверов в выбранной локации."
                e is kotlinx.coroutines.TimeoutCancellationException || m.contains("timed out") || m.contains("timeout") ->
                    "Не удалось подключиться.\nПревышено время ожидания."
                m.contains("auth") || m.contains("ticket") || m.contains("лиценз") ->
                    "Ошибка авторизации."
                m.contains("typeconfig") ->
                    "Не удалось получить настройки VPN."
                m.contains("tun") || m.contains("vpn-интерфейс") || m.contains("интерфейс") ->
                    "Не удалось создать VPN-туннель."
                m.contains("protect") || m.contains("quic") || m.contains("dial") || m.contains("transport") ->
                    "Сервер недоступен."
                else -> "Не удалось подключиться.\nСервер недоступен."
            }
        }
    }
}
