package cyou.rime.telemetry.collector

import android.content.Context
import java.net.HttpURLConnection
import java.net.URL

/** Calls a paired device makes to the Telemetry server. */
object DeviceApi {
    /** Returns the response body, or null when offline, unpaired or refused. Blocking IO. */
    fun get(context: Context, path: String): String? = call(context, "GET", path)

    /** A call that changes something; true when the server accepted it. Blocking IO. */
    fun post(context: Context, path: String): Boolean = call(context, "POST", path) != null

    private fun call(context: Context, method: String, path: String): String? {
        val config = DeliveryConfig(context)
        val token = config.token() ?: return null
        return runCatching {
            val connection = URL(config.serverUrl.trimEnd('/') + path).openConnection() as HttpURLConnection
            try {
                connection.requestMethod = method
                connection.connectTimeout = 10_000
                connection.readTimeout = 10_000
                connection.setRequestProperty("Authorization", "Bearer $token")
                connection.setRequestProperty("X-Telemetry-Device-ID", DeviceIdentity.get(context))
                connection.setRequestProperty("Accept", "application/json")
                check(connection.responseCode == 200) { "$path failed with ${connection.responseCode}" }
                connection.inputStream.bufferedReader().use { it.readText() }
            } finally {
                connection.disconnect()
            }
        }.getOrNull()
    }
}
