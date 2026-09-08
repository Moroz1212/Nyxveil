package ru.nyxveil.android

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch
import ru.nyxveil.android.ui.AppViewModel
import ru.nyxveil.android.ui.NyxveilApp
import ru.nyxveil.android.ui.theme.NyxveilTheme

class MainActivity : ComponentActivity() {
    private val viewModel: AppViewModel by viewModels { AppViewModel.Factory(application) }

    private var pendingVpnGrant: (() -> Unit)? = null

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        val cont = pendingVpnGrant
        pendingVpnGrant = null
        if (result.resultCode == RESULT_OK) {
            cont?.invoke()
        } else {
            viewModel.onVpnPermissionDenied()
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        viewModel.attachVpnPermissionHandler { onGranted ->
            val prepare = VpnService.prepare(this)
            if (prepare == null) {
                onGranted()
            } else {
                pendingVpnGrant = onGranted
                vpnPermissionLauncher.launch(prepare)
            }
        }
        lifecycleScope.launch {
            viewModel.bootstrap()
        }
        setContent {
            NyxveilTheme {
                val ui by viewModel.uiState.collectAsState()
                NyxveilApp(
                    state = ui,
                    onSubmitLicense = { key -> viewModel.submitLicense(key) },
                    onConnect = { viewModel.connect() },
                    onDisconnect = { viewModel.disconnect() },
                    onSelectLocation = { id -> viewModel.selectLocation(id) },
                    onRefreshCatalog = { viewModel.refreshCatalog() },
                    onToggleAutoConnect = { viewModel.setAutoConnect(it) },
                    onToggleNotifications = { viewModel.setNotificationsEnabled(it) },
                    onRefreshDiagnostics = { viewModel.refreshDiagnostics() },
                )
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }
}
