package cyou.rime.telemetry.collector

import android.content.Context

class PolicyStore(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(NAME, Context.MODE_PRIVATE)

    var captureNotifications: Boolean
        get() = preferences.getBoolean(KEY_CAPTURE_NOTIFICATIONS, false)
        set(value) = preferences.edit().putBoolean(KEY_CAPTURE_NOTIFICATIONS, value).apply()

    var softBlockEnabled: Boolean
        get() = preferences.getBoolean(KEY_SOFT_BLOCK_ENABLED, false)
        set(value) = preferences.edit().putBoolean(KEY_SOFT_BLOCK_ENABLED, value).apply()

    var blockedPackages: Set<String>
        get() = preferences.getStringSet(KEY_BLOCKED_PACKAGES, emptySet()).orEmpty()
        set(value) = preferences.edit().putStringSet(KEY_BLOCKED_PACKAGES, value).apply()

    private companion object {
        const val NAME = "telemetry-policy"
        const val KEY_CAPTURE_NOTIFICATIONS = "capture_notifications"
        const val KEY_SOFT_BLOCK_ENABLED = "soft_block_enabled"
        const val KEY_BLOCKED_PACKAGES = "blocked_packages"
    }
}
