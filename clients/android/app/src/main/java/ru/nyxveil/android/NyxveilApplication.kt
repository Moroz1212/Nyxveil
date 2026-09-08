package ru.nyxveil.android

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import ru.nyxveil.android.logging.AppLog
import ru.nyxveil.android.network.NetworkMonitor
import ru.nyxveil.android.settings.AppSettings
import ru.nyxveil.android.vpn.VpnController

class NyxveilApplication : Application() {
    lateinit var appSettings: AppSettings
        private set
    lateinit var networkMonitor: NetworkMonitor
        private set
    lateinit var vpnController: VpnController
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        AppLog.init(this)
        AppLog.i(ru.nyxveil.android.logging.LogCategory.APP, "Application start")
        appSettings = AppSettings(this)
        networkMonitor = NetworkMonitor(this)
        vpnController = VpnController(this)
        ensureVpnNotificationChannel()
        networkMonitor.start()
    }

    private fun ensureVpnNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val mgr = getSystemService(NotificationManager::class.java) ?: return
        val channel = NotificationChannel(
            getString(R.string.vpn_notification_channel),
            getString(R.string.vpn_notification_channel_name),
            NotificationManager.IMPORTANCE_LOW,
        )
        mgr.createNotificationChannel(channel)
    }

    companion object {
        @Volatile
        lateinit var instance: NyxveilApplication
            private set
    }
}
