package ru.nyxveil.android.settings

import android.content.Context
import android.content.SharedPreferences

/** Simple SharedPreferences settings (autoConnect, notifications). */
class AppSettings(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    var autoConnect: Boolean
        get() = prefs.getBoolean(KEY_AUTO_CONNECT, false)
        set(value) = prefs.edit().putBoolean(KEY_AUTO_CONNECT, value).apply()

    var notificationsEnabled: Boolean
        get() = prefs.getBoolean(KEY_NOTIFICATIONS, true)
        set(value) = prefs.edit().putBoolean(KEY_NOTIFICATIONS, value).apply()

    var selectedLocationId: String
        get() = prefs.getString(KEY_LOCATION, "") ?: ""
        set(value) = prefs.edit().putString(KEY_LOCATION, value).apply()

    companion object {
        private const val PREFS = "nyxveil_settings"
        private const val KEY_AUTO_CONNECT = "auto_connect"
        private const val KEY_NOTIFICATIONS = "notifications_enabled"
        private const val KEY_LOCATION = "selected_location_id"
    }
}
