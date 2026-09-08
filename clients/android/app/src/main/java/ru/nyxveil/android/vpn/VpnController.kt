package ru.nyxveil.android.vpn

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.net.VpnService
import android.os.IBinder
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Binds/starts [NyxveilVpnService]. Single session; disconnect is idempotent.
 */
class VpnController(private val appContext: Context) {
    private val mutex = Mutex()
    private var binder: NyxveilVpnService.LocalBinder? = null
    private var binding: CompletableDeferred<NyxveilVpnService.LocalBinder>? = null

    private val _state = MutableStateFlow(ConnectionState.Disconnected)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, service: IBinder?) {
            val b = service as NyxveilVpnService.LocalBinder
            binder = b
            binding?.complete(b)
            b.service.stateFlow.value.let { _state.value = it }
            b.service.setStateListener { _state.value = it }
        }

        override fun onServiceDisconnected(name: ComponentName?) {
            binder = null
            _state.value = ConnectionState.Disconnected
        }
    }

    fun prepareIntent(activityContext: Context): Intent? = VpnService.prepare(activityContext)

    suspend fun connect(request: PreparedConnectRequest) = mutex.withLock {
        val svc = ensureBound()
        svc.connectSession(request)
    }

    suspend fun disconnect() = mutex.withLock {
        val svc = binder?.service
        if (svc != null) {
            svc.disconnectSession()
        }
        _state.value = ConnectionState.Disconnected
    }

    fun snapshot(): NyxveilVpnService.SessionSnapshot? = binder?.service?.snapshot()

    private suspend fun ensureBound(): NyxveilVpnService {
        binder?.service?.let { return it }
        val deferred = CompletableDeferred<NyxveilVpnService.LocalBinder>()
        binding = deferred
        val intent = Intent(appContext, NyxveilVpnService::class.java)
        appContext.startForegroundService(intent)
        val ok = appContext.bindService(intent, connection, Context.BIND_AUTO_CREATE)
        if (!ok) {
            binding = null
            throw IllegalStateException("Не удалось запустить VPN-службу.")
        }
        val b = deferred.await()
        binding = null
        return b.service
    }
}
