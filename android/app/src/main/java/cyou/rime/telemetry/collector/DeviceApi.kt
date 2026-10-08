package cyou.rime.telemetry.collector

import android.content.Context
import java.net.HttpURLConnection
import java.net.URL

/** Calls a paired device makes to the Telemetry server. */
object DeviceApi {
    /** Returns the response body, or null when offline, unpaired or refused. Blocking IO. */
    fun get(context: Context, path: String): String? = call(context, "GET", path, null)

    /** A call that changes something, with an optional JSON body; true when the server accepted it. Blocking IO. */
    fun post(context: Context, path: String, body: String? = null): Boolean = call(context, "POST", path, body) != null

    /**
     * Waits up to 50 seconds for the answer to /v1/device/now to differ from
     * [fingerprint]. Returns the new body, an empty string when nothing
     * changed, or null when the server could not be reached. Blocking IO.
     */
    fun waitNow(context: Context, fingerprint: String): String? {
        val config = DeliveryConfig(context)
        val token = config.token() ?: return null
        val path = "/v1/device/now?timeout=50&wait=" + java.net.URLEncoder.encode(fingerprint, "UTF-8")
        val headers = mapOf("Authorization" to "Bearer $token", "X-Telemetry-Device-ID" to DeviceIdentity.get(context), "Accept" to "application/json")
        SealedChannel.configured()?.let { channel ->
            val answer = try {
                channel.exchange("GET", path, headers, null, 10_000, 75_000)
            } catch (_: java.io.IOException) {
                null
            }
            if (answer != null) return when (answer.status) {
                200 -> String(answer.body, Charsets.UTF_8)
                304 -> ""
                else -> null
            }
        }
        return runCatching {
            val connection = URL(config.serverUrl.trimEnd('/') + path).openConnection() as HttpURLConnection
            try {
                connection.connectTimeout = 10_000
                connection.readTimeout = 75_000
                headers.forEach { (name, value) -> connection.setRequestProperty(name, value) }
                when (connection.responseCode) {
                    200 -> connection.inputStream.bufferedReader().use { it.readText() }
                    304 -> ""
                    else -> null
                }
            } finally {
                connection.disconnect()
            }
        }.getOrNull()
    }

    private fun call(context: Context, method: String, path: String, body: String?): String? {
        val config = DeliveryConfig(context)
        val token = config.token() ?: return null
        // Through the sealed relay first; the direct address (Tailscale) only
        // when the relay cannot be reached.
        SealedChannel.configured()?.let { channel ->
            val headers = mutableMapOf(
                "Authorization" to "Bearer $token",
                "X-Telemetry-Device-ID" to DeviceIdentity.get(context),
                "Accept" to "application/json",
            )
            if (body != null) headers["Content-Type"] = "application/json"
            val answer = try {
                channel.exchange(method, path, headers, body?.toByteArray(), 10_000, 10_000)
            } catch (_: java.io.IOException) {
                null
            }
            if (answer != null) return if (answer.status == 200) String(answer.body, Charsets.UTF_8) else null
        }
        return runCatching {
            val connection = URL(config.serverUrl.trimEnd('/') + path).openConnection() as HttpURLConnection
            try {
                connection.requestMethod = method
                connection.connectTimeout = 10_000
                connection.readTimeout = 10_000
                connection.setRequestProperty("Authorization", "Bearer $token")
                connection.setRequestProperty("X-Telemetry-Device-ID", DeviceIdentity.get(context))
                connection.setRequestProperty("Accept", "application/json")
                if (body != null) {
                    connection.doOutput = true
                    connection.setRequestProperty("Content-Type", "application/json")
                    connection.outputStream.use { it.write(body.toByteArray()) }
                }
                check(connection.responseCode == 200) { "$path failed with ${connection.responseCode}" }
                connection.inputStream.bufferedReader().use { it.readText() }
            } finally {
                connection.disconnect()
            }
        }.getOrNull()
    }
}
