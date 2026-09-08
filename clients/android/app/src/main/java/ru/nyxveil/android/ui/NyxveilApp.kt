package ru.nyxveil.android.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.BugReport
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.ListAlt
import androidx.compose.material.icons.outlined.Public
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.sp
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import ru.nyxveil.android.R
import ru.nyxveil.android.logging.AppLog
import ru.nyxveil.android.ui.diagnostics.DiagnosticsScreen
import ru.nyxveil.android.ui.home.HomeScreen
import ru.nyxveil.android.ui.license.LicenseScreen
import ru.nyxveil.android.ui.logs.LogsScreen
import ru.nyxveil.android.ui.servers.ServersScreen
import ru.nyxveil.android.ui.settings.SettingsScreen
import ru.nyxveil.android.ui.theme.NyxNavy

private object Routes {
    const val License = "license"
    const val Home = "home"
    const val Servers = "servers"
    const val Logs = "logs"
    const val Settings = "settings"
    const val Diagnostics = "diagnostics"
}

@Composable
fun NyxveilApp(
    state: AppUiState,
    onSubmitLicense: (String) -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onSelectLocation: (String) -> Unit,
    onRefreshCatalog: () -> Unit,
    onToggleAutoConnect: (Boolean) -> Unit,
    onToggleNotifications: (Boolean) -> Unit,
    onRefreshDiagnostics: () -> Unit,
    onClearLogs: () -> Unit = { AppLog.clear() },
) {
    if (!state.hasLicense) {
        LicenseScreen(
            loading = state.licenseLoading,
            error = state.licenseError,
            onContinue = onSubmitLicense,
        )
        return
    }

    val navController = rememberNavController()
    val backStack by navController.currentBackStackEntryAsState()
    val current = backStack?.destination
    val logEntries by AppLog.entries.collectAsState()

    Scaffold(
        containerColor = NyxNavy,
        bottomBar = {
            NavigationBar(containerColor = NyxNavy) {
                NavigationBarItem(
                    selected = current?.hierarchy?.any { it.route == Routes.Home } == true,
                    onClick = { navController.navigate(Routes.Home) { launchSingleTop = true } },
                    icon = { Icon(Icons.Outlined.Home, contentDescription = null) },
                    label = { NavLabel(stringResource(R.string.nav_home)) },
                )
                NavigationBarItem(
                    selected = current?.hierarchy?.any { it.route == Routes.Servers } == true,
                    onClick = { navController.navigate(Routes.Servers) { launchSingleTop = true } },
                    icon = { Icon(Icons.Outlined.Public, contentDescription = null) },
                    label = { NavLabel(stringResource(R.string.nav_servers)) },
                )
                NavigationBarItem(
                    selected = current?.hierarchy?.any { it.route == Routes.Logs } == true,
                    onClick = { navController.navigate(Routes.Logs) { launchSingleTop = true } },
                    icon = { Icon(Icons.Outlined.ListAlt, contentDescription = null) },
                    label = { NavLabel(stringResource(R.string.nav_logs)) },
                )
                NavigationBarItem(
                    selected = current?.hierarchy?.any { it.route == Routes.Settings } == true,
                    onClick = { navController.navigate(Routes.Settings) { launchSingleTop = true } },
                    icon = { Icon(Icons.Outlined.Settings, contentDescription = null) },
                    label = { NavLabel(stringResource(R.string.nav_settings)) },
                )
                NavigationBarItem(
                    selected = current?.hierarchy?.any { it.route == Routes.Diagnostics } == true,
                    onClick = {
                        onRefreshDiagnostics()
                        navController.navigate(Routes.Diagnostics) { launchSingleTop = true }
                    },
                    icon = { Icon(Icons.Outlined.BugReport, contentDescription = null) },
                    label = { NavLabel(stringResource(R.string.nav_diagnostics)) },
                )
            }
        },
    ) { padding ->
        NavHost(
            navController = navController,
            startDestination = Routes.Home,
            modifier = Modifier.padding(padding),
        ) {
            composable(Routes.Home) {
                HomeScreen(
                    state = state,
                    onConnect = onConnect,
                    onDisconnect = onDisconnect,
                )
            }
            composable(Routes.Servers) {
                ServersScreen(
                    locations = state.locations,
                    selectedLocationId = state.selectedLocationId,
                    refreshing = state.catalogLoading,
                    error = state.catalogError,
                    onSelect = onSelectLocation,
                    onRefresh = onRefreshCatalog,
                )
            }
            composable(Routes.Logs) {
                LogsScreen(entries = logEntries, onClear = onClearLogs)
            }
            composable(Routes.Settings) {
                SettingsScreen(
                    autoConnect = state.autoConnect,
                    notificationsEnabled = state.notificationsEnabled,
                    appVersion = state.appVersion,
                    onAutoConnectChange = onToggleAutoConnect,
                    onNotificationsChange = onToggleNotifications,
                )
            }
            composable(Routes.Diagnostics) {
                DiagnosticsScreen(snapshot = state.diagnostics)
            }
        }
    }
}

@Composable
private fun NavLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall.copy(fontSize = 11.sp, lineHeight = 12.sp),
        maxLines = 1,
        softWrap = false,
        overflow = TextOverflow.Clip,
    )
}
