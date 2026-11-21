package cyou.rime.telemetry.collector

import android.content.Context
import android.provider.Settings
import java.security.MessageDigest

/**
 * This phone's device ID. It comes from Android's ID for the app, which a
 * format changes; so when the server says this is a phone it already knows
 * (set up again after a format), the phone adopts that ID and stays one phone.
 */
object DeviceIdentity {
    private const val PREFS = "telemetry-identity"
    private const val KEY = "device_id"
    private val FORMAT = Regex("android-[0-9a-f]{16}")

    fun get(context: Context): String =
        context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null) ?: derived(context)

    /** The ID this install would have on its own. */
    fun derived(context: Context): String {
        val raw = Settings.Secure.getString(
            context.contentResolver,
            Settings.Secure.ANDROID_ID,
        ).orEmpty().ifBlank { "unknown-device" }
        val digest = MessageDigest.getInstance("SHA-256")
            .digest(raw.toByteArray(Charsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
        return "android-${digest.take(16)}"
    }

    /** Takes the ID the server registered this phone as, moving unsent records to it. */
    fun adopt(context: Context, id: String): Boolean {
        val current = get(context)
        if (id == current || !FORMAT.matches(id)) return false
        check(context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY, id).commit())
        HealthQueue(context).reassignDevice(current, id)
        return true
    }

    /**
     * Records made under the install's own ID after adopting another (by a
     * part of the app that read the ID before) are moved before sending.
     */
    fun settle(context: Context) {
        val current = get(context)
        val own = derived(context)
        if (current != own) HealthQueue(context).reassignDevice(own, current)
    }
}
