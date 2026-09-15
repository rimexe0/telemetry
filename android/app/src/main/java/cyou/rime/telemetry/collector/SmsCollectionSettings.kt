package cyou.rime.telemetry.collector

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat

/**
 * SMS is an explicit owner opt-in. A manifest declaration or a granted Android
 * permission does not enable collection by itself. (R-003, R-009, R-012.)
 */
class SmsCollectionSettings(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences(
        PREFERENCES_NAME,
        Context.MODE_PRIVATE,
    )

    var enabled: Boolean
        get() = preferences.getBoolean(KEY_ENABLED, false)
        set(value) {
            check(preferences.edit().putBoolean(KEY_ENABLED, value).commit()) {
                "Unable to persist SMS collection setting"
            }
        }

    companion object {
        const val PREFERENCES_NAME = "telemetry-sms-collection"
        const val KEY_ENABLED = "enabled"

        fun hasReadPermission(context: Context): Boolean = ContextCompat.checkSelfPermission(
            context,
            Manifest.permission.READ_SMS,
        ) == PackageManager.PERMISSION_GRANTED
    }
}
