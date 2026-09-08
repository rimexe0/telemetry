package cyou.rime.telemetry.collector

import android.content.Context
import android.provider.Settings
import java.security.MessageDigest

object DeviceIdentity {
    fun get(context: Context): String {
        val raw = Settings.Secure.getString(
            context.contentResolver,
            Settings.Secure.ANDROID_ID,
        ).orEmpty().ifBlank { "unknown-device" }
        val digest = MessageDigest.getInstance("SHA-256")
            .digest(raw.toByteArray(Charsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
        return "android-${digest.take(16)}"
    }
}
