package ru.nyxveil.android.ui

import android.app.Application
import android.net.VpnService
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import ru.nyxveil.android.BuildConfig
import ru.nyxveil.android.NyxveilApplication
import ru.nyxveil.android.catalog.CatalogRepository
import ru.nyxveil.android.catalog.CatalogSignatureException
import ru.nyxveil.android.catalog.CatalogTemporalException
import ru.nyxveil.android.catalog.LocationUi
import ru.nyxveil.android.diagnostics.DiagSnapshot
import ru.nyxveil.android.licensing.ControlPlaneClient
import ru.nyxveil.android.licensing.DeviceIdentityStore
import ru.nyxveil.android.licensing.LicenseRepository
import ru.nyxveil.android.vpn.ConnectionState
import ru.nyxveil.android.vpn.SessionDesire
import ru.nyxveil.android.network.PhysicalNetworkTracker
import ru.nyxveil.bridge.NvpBridge
import ru.nyxveil.android.logging.AppLog
import android.util.Log
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

data class AppUiState(
    val hasLicense: Boolean = false,
    val licenseLoading: Boolean = false,
    val licenseError: String? = null,
    val catalogLoading: Boolean = false,
    val catalogError: String? = null,
    val locations: List<LocationUi> = emptyList(),
    val selectedLocationId: String = "",
    val selectedLocationLabel: String = "",
    val selectedLocationCountry: String = "",
    val selectedLocationCity: String = "",
    val connectionState: ConnectionState = ConnectionState.Disconnected,
    val statusMessage: String? = null,
    val pingMs: Int? = null,
    val downloadMbps: Double? = null,
    val uploadMbps: Double? = null,
    val vpnIp: String = "",
    val sessionStartedAtMs: Long = 0,
    val transport: String = "",
    val mtu: Int = 0,
    val dns: String = "",
    val autoConnect: Boolean = false,
    val notificationsEnabled: Boolean = true,
    val appVersion: String = BuildConfig.VERSION_NAME,
    val diagnostics: DiagSnapshot = DiagSnapshot(),
)

class AppViewModel(app: Application) : AndroidViewModel(app) {
    private val appCtx = getApplication<NyxveilApplication>()
    private val licenseRepo = LicenseRepository(appCtx)
    private val deviceStore = DeviceIdentityStore(appCtx)
    private val cp = ControlPlaneClient()
    private val catalogRepo = CatalogRepository(appCtx, cp)
    private val settings = appCtx.appSettings
    private val vpn = appCtx.vpnController

    private val _ui = MutableStateFlow(
        AppUiState(
            hasLicense = licenseRepo.hasLicense(),
            autoConnect = settings.autoConnect,
            notificationsEnabled = settings.notificationsEnabled,
            selectedLocationId = settings.selectedLocationId,
        ),
    )
    val uiState: StateFlow<AppUiState> = _ui.asStateFlow()

    private var vpnPermissionHandler: ((onGranted: () -> Unit) -> Unit)? = null
    private var lastCatalogStatus: String = "—"
    private var identity = deviceStore.getOrCreate()
    private val sessionDesire = SessionDesire()
    private val reconnectMutex = Mutex()
    private var pathReconnectJob: Job? = null

    init {
        viewModelScope.launch {
            vpn.state.collect { st ->
                if (st == ConnectionState.Disconnected) {
                    sessionDesire.onObservedDisconnected()
                }
                val snap = vpn.snapshot()
                val loc = _ui.value.locations.find { it.locationId == _ui.value.selectedLocationId }
                _ui.update {
                    it.copy(
                        connectionState = st,
                        vpnIp = snap?.vpnIp.orEmpty(),
                        pingMs = snap?.pingMs,
                        downloadMbps = snap?.downloadMbps,
                        uploadMbps = snap?.uploadMbps,
                        statusMessage = when {
                            st == ConnectionState.Connected -> null
                            st == ConnectionState.Disconnected -> null
                            !snap?.message.isNullOrBlank() -> snap?.message
                            else -> it.statusMessage
                        },
                        sessionStartedAtMs = snap?.connectedAtMs ?: 0L,
                        transport = snap?.transport.orEmpty(),
                        mtu = snap?.mtu ?: 0,
                        dns = snap?.dns.orEmpty(),
                        selectedLocationCountry = loc?.country.orEmpty(),
                        selectedLocationCity = loc?.city.orEmpty(),
                    )
                }
            }
        }
        viewModelScope.launch {
            while (true) {
                kotlinx.coroutines.delay(1000)
                if (_ui.value.connectionState == ConnectionState.Connected) {
                    val snap = vpn.snapshot() ?: continue
                    _ui.update {
                        it.copy(
                            vpnIp = snap.vpnIp,
                            pingMs = snap.pingMs,
                            downloadMbps = snap.downloadMbps,
                            uploadMbps = snap.uploadMbps,
                            mtu = snap.mtu,
                            dns = snap.dns,
                            transport = snap.transport,
                            sessionStartedAtMs = snap.connectedAtMs,
                            statusMessage = null,
                        )
                    }
                } else if (_ui.value.connectionState == ConnectionState.Error) {
                    val snap = vpn.snapshot()
                    if (!snap?.message.isNullOrBlank()) {
                        _ui.update { it.copy(statusMessage = snap?.message) }
                    }
                }
            }
        }
        appCtx.networkMonitor.setPathChangeListener { event ->
            when (event.kind) {
                PhysicalNetworkTracker.EventKind.CHANGED ->
                    AppLog.i(
                        ru.nyxveil.android.logging.LogCategory.APP,
                        "Network path changed ${event.label}",
                    )
                PhysicalNetworkTracker.EventKind.LOST ->
                    AppLog.i(
                        ru.nyxveil.android.logging.LogCategory.APP,
                        "Network path lost",
                    )
            }
            schedulePathReconnect(event)
        }
    }

    private fun schedulePathReconnect(event: PhysicalNetworkTracker.Event) {
        if (!sessionDesire.shouldReconnectOnPathEvent(_ui.value.connectionState)) {
            return
        }
        // Cosmetic: additional network already filtered by tracker; only LOST / real CHANGED remain.
        pathReconnectJob?.cancel()
        pathReconnectJob = viewModelScope.launch {
            delay(900)
            if (!sessionDesire.shouldReconnectOnPathEvent(_ui.value.connectionState)) return@launch
            reconnectMutex.withLock {
                if (!sessionDesire.beginReconnect()) return@withLock
                try {
                    AppLog.i(
                        ru.nyxveil.android.logging.LogCategory.RECONNECT,
                        "physical path ${event.kind} label=${event.label}",
                    )
                    _ui.update {
                        it.copy(
                            connectionState = ConnectionState.Reconnecting,
                            statusMessage = null,
                        )
                    }
                    vpn.disconnect()
                    // Keep userDesiredConnected; connect() would re-assert it.
                    connectInternal(userInitiated = false)
                } finally {
                    sessionDesire.endReconnect()
                }
            }
        }
    }

    fun attachVpnPermissionHandler(handler: (onGranted: () -> Unit) -> Unit) {
        vpnPermissionHandler = handler
    }

    fun onVpnPermissionDenied() {
        _ui.update {
            it.copy(
                connectionState = ConnectionState.Error,
                statusMessage = "Разрешение VPN отклонено.",
            )
        }
    }

    suspend fun bootstrap() {
        if (!licenseRepo.hasLicense()) return
        _ui.update { it.copy(hasLicense = true) }
        refreshCatalog()
        val st = _ui.value.connectionState
        if (settings.autoConnect &&
            _ui.value.selectedLocationId.isNotBlank() &&
            st != ConnectionState.Connected &&
            !st.isBusy()
        ) {
            connect()
        }
    }

    fun submitLicense(rawKey: String) {
        viewModelScope.launch {
            val key = rawKey.trim()
            if (key.isEmpty()) {
                _ui.update { it.copy(licenseError = "Введите лицензионный ключ.") }
                return@launch
            }
            _ui.update { it.copy(licenseLoading = true, licenseError = null) }
            try {
                val validated = cp.validateLicense(key)
                if (!validated.valid) {
                    _ui.update {
                        it.copy(
                            licenseLoading = false,
                            licenseError = validated.message?.takeIf { m -> m.isNotBlank() }
                                ?: "Лицензия недействительна.",
                        )
                    }
                    return@launch
                }
                identity = deviceStore.getOrCreate()
                cp.activateDevice(
                    licenseToken = key,
                    deviceId = identity.deviceId,
                    publicKeyRaw32 = identity.publicKeyRaw32,
                    deviceName = identity.deviceName,
                )
                licenseRepo.saveLicenseToken(key)
                _ui.update {
                    it.copy(
                        hasLicense = true,
                        licenseLoading = false,
                        licenseError = null,
                    )
                }
                refreshCatalog()
            } catch (e: ControlPlaneClient.ControlPlaneException) {
                _ui.update { it.copy(licenseLoading = false, licenseError = e.message) }
            } catch (_: Exception) {
                _ui.update {
                    it.copy(
                        licenseLoading = false,
                        licenseError = "Не удалось проверить лицензию. Повторите попытку.",
                    )
                }
            }
        }
    }

    fun refreshCatalog() {
        if (!catalogRepo.tryBeginRefresh()) {
            Log.i("NYX-CATALOG", "stage=refresh result=SKIP_IN_FLIGHT")
            return
        }
        viewModelScope.launch {
            val token = licenseRepo.getLicenseToken()
            if (token == null) {
                catalogRepo.endRefresh()
                return@launch
            }
            _ui.update { it.copy(catalogLoading = true, catalogError = null) }
            try {
                val loaded = catalogRepo.fetchAndVerify(token)
                val locations = catalogRepo.locationsGroupedByCity(loaded.signed.catalog)
                val diag = catalogRepo.lastDiag
                lastCatalogStatus = diag.toUserCatalogLine().ifBlank {
                    buildString {
                        append(if (loaded.fromCache) "кэш" else "сеть")
                        append(" · готов")
                    }
                }
                var selected = _ui.value.selectedLocationId
                if (selected.isBlank() || locations.none { it.locationId == selected }) {
                    selected = locations.firstOrNull()?.locationId.orEmpty()
                    settings.selectedLocationId = selected
                }
                val label = locations.find { it.locationId == selected }?.displayName.orEmpty()
                _ui.update {
                    it.copy(
                        catalogLoading = false,
                        locations = locations,
                        selectedLocationId = selected,
                        selectedLocationLabel = label,
                        catalogError = null,
                    )
                }
            } catch (e: CatalogTemporalException) {
                lastCatalogStatus = catalogRepo.lastDiag.toUserCatalogLine().ifBlank { "истёк" }
                _ui.update {
                    it.copy(
                        catalogLoading = false,
                        catalogError = e.message
                            ?: "Срок действия каталога истёк. Нажмите «Обновить».",
                    )
                }
            } catch (e: CatalogSignatureException) {
                lastCatalogStatus = catalogRepo.lastDiag.toUserCatalogLine().ifBlank { "ошибка подписи" }
                _ui.update {
                    it.copy(
                        catalogLoading = false,
                        catalogError = e.message ?: "Не удалось проверить данные серверов.",
                    )
                }
            } catch (e: ControlPlaneClient.ControlPlaneException) {
                lastCatalogStatus = catalogRepo.lastDiag.toUserCatalogLine().ifBlank { "ошибка сети" }
                _ui.update {
                    it.copy(
                        catalogLoading = false,
                        catalogError = e.message
                            ?: "Не удалось загрузить список локаций.\nПроверьте подключение к интернету и повторите попытку.",
                    )
                }
            } catch (e: Exception) {
                Log.e("NYX-CATALOG", "stage=refresh result=FAIL class=${e.javaClass.simpleName}")
                lastCatalogStatus = catalogRepo.lastDiag.toUserCatalogLine().ifBlank { "ошибка" }
                _ui.update {
                    it.copy(
                        catalogLoading = false,
                        catalogError = "Не удалось загрузить список локаций.\nПроверьте подключение к интернету и повторите попытку.",
                    )
                }
            } finally {
                catalogRepo.endRefresh()
            }
        }
    }

    fun selectLocation(locationId: String) {
        val loc = _ui.value.locations.find { it.locationId == locationId }
        val label = loc?.displayName.orEmpty()
        settings.selectedLocationId = locationId
        _ui.update {
            it.copy(
                selectedLocationId = locationId,
                selectedLocationLabel = label,
                selectedLocationCity = loc?.city.orEmpty(),
                selectedLocationCountry = loc?.country.orEmpty(),
            )
        }
    }

    fun clearLogs() {
        AppLog.clear()
    }

    fun exportLogsText(): String = AppLog.snapshotText()

    fun exportLogsFile(): java.io.File = AppLog.exportFile(appCtx)

    fun setAutoConnect(enabled: Boolean) {
        settings.autoConnect = enabled
        _ui.update { it.copy(autoConnect = enabled) }
    }

    fun setNotificationsEnabled(enabled: Boolean) {
        settings.notificationsEnabled = enabled
        _ui.update { it.copy(notificationsEnabled = enabled) }
    }

    fun connect() {
        if (_ui.value.connectionState.isBusy()) {
            AppLog.w(ru.nyxveil.android.logging.LogCategory.VPN, "Connect ignored — already busy")
            return
        }
        if (_ui.value.connectionState == ConnectionState.Connected) {
            AppLog.w(ru.nyxveil.android.logging.LogCategory.VPN, "Connect ignored — already connected")
            return
        }
        sessionDesire.onUserConnectRequested()
        viewModelScope.launch {
            connectInternal(userInitiated = true)
        }
    }

    private suspend fun connectInternal(userInitiated: Boolean) {
        val token = licenseRepo.getLicenseToken()
        if (token.isNullOrBlank()) {
            _ui.update { it.copy(statusMessage = "Лицензия не найдена.") }
            return
        }
        val locationId = _ui.value.selectedLocationId
        if (locationId.isBlank()) {
            _ui.update { it.copy(statusMessage = "Выберите локацию.") }
            return
        }
        val label = _ui.value.selectedLocationLabel
        AppLog.i(ru.nyxveil.android.logging.LogCategory.VPN, "Connect requested location=$locationId")
        _ui.update {
            it.copy(connectionState = ConnectionState.Preparing, statusMessage = null)
        }
        val runConnect = suspend {
            try {
                if (!NvpBridge.nativeAvailable()) {
                    val why = NvpBridge.loadError() ?: "native unavailable"
                    _ui.update {
                        it.copy(
                            connectionState = ConnectionState.Error,
                            statusMessage = "Нативный NVP движок недоступен ($why).",
                        )
                    }
                } else {
                    _ui.update {
                        it.copy(
                            connectionState = ConnectionState.Preparing,
                            statusMessage = "Подготовка каталога…",
                        )
                    }
                    val bundle = kotlinx.coroutines.withTimeout(45_000) {
                        catalogRepo.prepareConnectCatalog(token)
                    }
                    val locStillValid = bundle.signed.catalog.locations.any {
                        it.locationId == locationId && it.enabled
                    }
                    if (!locStillValid) {
                        throw IllegalStateException("Нет доступных серверов в выбранной локации.")
                    }
                    _ui.update {
                        it.copy(
                            connectionState = ConnectionState.Preparing,
                            statusMessage = "Выдача доступа…",
                            locations = catalogRepo.locationsGroupedByCity(bundle.signed.catalog),
                        )
                    }
                    AppLog.i(ru.nyxveil.android.logging.LogCategory.CONTROLPLANE, "Access ticket request")
                    val ticket = kotlinx.coroutines.withTimeout(30_000) {
                        cp.issueTicket(
                            licenseToken = token,
                            deviceId = identity.deviceId,
                            locationId = locationId,
                        )
                    }
                    AppLog.i(ru.nyxveil.android.logging.LogCategory.CONTROLPLANE, "Access ticket issued")
                    val prepared = ru.nyxveil.android.vpn.PreparedConnectRequest(
                        locationId = locationId,
                        locationLabel = label,
                        accessTicket = ticket.accessTicket,
                        rawSignedCatalog = bundle.rawSignedCatalog,
                        catalogKeys = bundle.keys.keys,
                        devicePrivateKeySeed32 = identity.privateKeySeed32(),
                        catalogRawHash12 = bundle.rawHash12,
                        catalogSource = bundle.source,
                    )
                    AppLog.i(
                        ru.nyxveil.android.logging.LogCategory.VPN,
                        "PreparedConnect raw_hash=${prepared.catalogRawHash12} source=${prepared.catalogSource}",
                    )
                    _ui.update { it.copy(statusMessage = null) }
                    vpn.connect(prepared)
                    lastCatalogStatus = buildString {
                        append(if (bundle.source == "cache") "кэш" else "сеть")
                        append(" · v").append(bundle.signed.catalog.version)
                        append(" · ").append(bundle.rawHash12)
                    }
                }
            } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
                AppLog.e(ru.nyxveil.android.logging.LogCategory.ERROR, "Connect timeout", e)
                _ui.update {
                    it.copy(
                        connectionState = ConnectionState.Error,
                        statusMessage = "Не удалось подключиться.\nПревышено время ожидания.",
                    )
                }
            } catch (e: CatalogTemporalException) {
                AppLog.e(ru.nyxveil.android.logging.LogCategory.CATALOG, e.message ?: "catalog temporal", e)
                _ui.update {
                    it.copy(
                        connectionState = ConnectionState.Error,
                        statusMessage = "Не удалось обновить данные серверов.",
                    )
                }
            } catch (e: CatalogSignatureException) {
                AppLog.e(ru.nyxveil.android.logging.LogCategory.CATALOG, e.message ?: "catalog signature", e)
                _ui.update {
                    it.copy(
                        connectionState = ConnectionState.Error,
                        statusMessage = "Не удалось проверить данные серверов.",
                    )
                }
            } catch (e: ControlPlaneClient.ControlPlaneException) {
                AppLog.e(ru.nyxveil.android.logging.LogCategory.CONTROLPLANE, e.message ?: "CP error", e)
                _ui.update {
                    it.copy(connectionState = ConnectionState.Error, statusMessage = e.message)
                }
            } catch (e: Exception) {
                AppLog.e(ru.nyxveil.android.logging.LogCategory.ERROR, e.message ?: "connect failed", e)
                _ui.update {
                    it.copy(
                        connectionState = ConnectionState.Error,
                        statusMessage = ru.nyxveil.android.vpn.NyxveilVpnService.userFacingConnectError(e),
                    )
                }
            }
        }
        val handler = vpnPermissionHandler
        if (userInitiated && handler != null) {
            handler {
                viewModelScope.launch { runConnect() }
            }
        } else {
            runConnect()
        }
    }

    fun disconnect() {
        sessionDesire.onUserDisconnectRequested()
        pathReconnectJob?.cancel()
        pathReconnectJob = null
        viewModelScope.launch {
            vpn.disconnect()
            _ui.update {
                it.copy(
                    connectionState = ConnectionState.Disconnected,
                    statusMessage = null,
                    vpnIp = "",
                    pingMs = null,
                    downloadMbps = null,
                    uploadMbps = null,
                    sessionStartedAtMs = 0,
                )
            }
        }
    }

    fun refreshDiagnostics() {
        val snap = vpn.snapshot()
        val vpnPerm = if (VpnService.prepare(appCtx) == null) "выдано" else "требуется"
        val licenseStatus = if (licenseRepo.hasLicense()) "активна" else "нет"
        val cat = catalogRepo.lastDiag
        val catalogLine = when {
            cat.state == ru.nyxveil.android.catalog.CatalogState.Ready ->
                cat.userSummary.ifBlank { lastCatalogStatus }
            cat.userSummary.isNotBlank() && cat.userSummary != "—" -> cat.userSummary
            else -> lastCatalogStatus
        }
        _ui.update {
            it.copy(
                diagnostics = DiagSnapshot(
                    licenseStatus = licenseStatus,
                    catalogStatus = catalogLine,
                    vpnPermissionStatus = vpnPerm,
                    protocolStatus = snap?.protocol?.ifBlank { "NVP/1" } ?: "NVP/1",
                    transportStatus = snap?.transport?.takeIf { it.isNotBlank() }?.let {
                        ru.nyxveil.android.ui.home.formatTransport(it)
                    } ?: "—",
                    tunnelStatus = when (it.connectionState) {
                        ConnectionState.Connected -> "активен"
                        ConnectionState.Disconnected -> "нет"
                        else -> it.connectionState.name
                    },
                    dnsStatus = snap?.dns?.ifBlank { "—" } ?: "—",
                    mtuStatus = snap?.mtu?.takeIf { m -> m > 0 }?.toString() ?: "—",
                    connectionState = it.connectionState.name,
                    deviceIdRedacted = deviceStore.redactDeviceId(identity.deviceId),
                    bridgeStatus = buildString {
                        append("v").append(NvpBridge.version())
                        append(" · ").append(NvpBridge.coreProtocol())
                        if (NvpBridge.nativeAvailable()) {
                            append(" · native")
                        } else {
                            append(" · unavailable")
                            NvpBridge.loadError()?.let { err -> append(" (").append(err).append(")") }
                        }
                    },
                    catalogSource = cat.source,
                    catalogKeyId = cat.keyId,
                    catalogSignatureStatus = cat.signatureStatus,
                    catalogTemporalStatus = cat.temporalStatus,
                    catalogHttpStage = cat.httpStage,
                    tunReadPackets = snap?.tunReadPackets?.toString() ?: "0",
                    nvpTxPackets = snap?.nvpTxPackets?.toString() ?: "0",
                    nvpRxPackets = snap?.nvpRxPackets?.toString() ?: "0",
                    tunWritePackets = snap?.tunWritePackets?.toString() ?: "0",
                    trafficStatus = when {
                        snap == null -> "—"
                        it.connectionState != ConnectionState.Connected -> "—"
                        snap.trafficIdle -> "idle"
                        else -> "active"
                    },
                    txPumpAlive = when {
                        snap == null -> "—"
                        snap.txPumpAlive -> "alive"
                        it.connectionState == ConnectionState.Connected -> "dead"
                        else -> "—"
                    },
                ),
            )
        }
    }

    class Factory(private val app: Application) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T {
            if (modelClass.isAssignableFrom(AppViewModel::class.java)) {
                return AppViewModel(app) as T
            }
            throw IllegalArgumentException("Unknown ViewModel")
        }
    }
}
