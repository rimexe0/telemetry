package cyou.rime.telemetry.collector

import android.net.Uri
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import org.json.JSONObject

data class PairingStart(
    val id: String,
    val panelUrl: String,
)

data class PairingStatus(
    val state: State,
    val deviceId: String? = null,
) {
    enum class State { PENDING, APPROVED, DENIED, EXPIRED }
}

class PairingException(
    val status: Int,
    message: String,
) : IllegalStateException(message)

/**
 * Small client for the Clerk-assisted pairing handshake.
 *
 * The pairing secret is generated and retained by DeliveryConfig. It is sent
 * only over the tailnet connection and is never included in UI status text.
 * The panel proves the owner's Clerk identity; this client only polls for the
 * resulting device-scoped ingest credential.
 */
class TelemetryPairingClient(
    private val serverUrl: String,
    private val deviceId: String,
    private val deviceName: String,
) {
    init {
        val u = URL(serverUrl)
        require(u.protocol == "https" && u.userInfo == null && u.query == null && u.ref == null && (u.path.isEmpty() || u.path == "/")) { "Pairing requires a private HTTPS origin" }
    }
    fun start(secret: String): PairingStart {
        val connection = open("/v1/pairing/sessions", "POST")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.doOutput = true
        val body = JSONObject()
            .put("device_id", deviceId)
            .put("device_name", deviceName)
            .put("credential_hash", MessageDigest.getInstance("SHA-256").digest(secret.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) })
        connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
        val payload = response(connection, setOf(HttpURLConnection.HTTP_OK, HttpURLConnection.HTTP_CREATED, HttpURLConnection.HTTP_ACCEPTED))
        val id = required(payload, "pairing_id", "id")
        val panelUrl = optional(payload, "panel_url", "pairing_url")
            ?: Uri.parse(serverUrl.trimEnd('/') + "/panel/")
                .buildUpon()
                .appendQueryParameter("pair", id)
                .build()
                .toString()
        val expected = URL(serverUrl)
        val target = URL(panelUrl)
        require(target.protocol == expected.protocol && target.host == expected.host && target.port == expected.port && target.userInfo == null && target.path == "/panel/") { "Pairing response changed server" }
        require(Uri.parse(panelUrl).getQueryParameter("pair") == id) { "Pairing response changed session" }
        return PairingStart(id, panelUrl)
    }

    fun poll(pairingId: String, secret: String): PairingStatus {
        val connection = open("/v1/pairing/sessions/${Uri.encode(pairingId)}", "GET")
        connection.setRequestProperty("Authorization", "Bearer $secret")
        val payload = response(connection, setOf(HttpURLConnection.HTTP_OK))
        val state = when (optional(payload, "status", "state")?.lowercase()) {
            "approved" -> PairingStatus.State.APPROVED
            "denied", "rejected", "revoked" -> PairingStatus.State.DENIED
            "expired" -> PairingStatus.State.EXPIRED
            "pending" -> PairingStatus.State.PENDING
            else -> throw IllegalStateException("Unknown pairing status")
        }
        return PairingStatus(
            state = state,
            deviceId = optional(payload, "device_id"),
        )
    }

    private fun open(path: String, method: String): HttpURLConnection {
        val connection = URL(serverUrl.trimEnd('/') + path).openConnection() as HttpURLConnection
        connection.requestMethod = method
        connection.instanceFollowRedirects = false
        connection.connectTimeout = 15_000
        connection.readTimeout = 30_000
        connection.setRequestProperty("Accept", "application/json")
        return connection
    }

    private fun response(connection: HttpURLConnection, accepted: Set<Int>): JSONObject {
        val status = connection.responseCode
        val stream = if (status in 200..299) connection.inputStream else connection.errorStream
        val bytes = stream?.use {
            val out = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(4096)
            while (out.size() <= 65536) {
                val count = it.read(buffer, 0, minOf(buffer.size, 65537 - out.size()))
                if (count < 0) break
                out.write(buffer, 0, count)
            }
            out.toByteArray()
        } ?: ByteArray(0)
        connection.disconnect()
        require(bytes.size <= 65536) { "Pairing response too large" }
        val text = bytes.toString(Charsets.UTF_8)
        if (status !in accepted) {
            throw PairingException(status, "Telemetry pairing returned HTTP $status")
        }
        return if (text.isBlank()) JSONObject() else JSONObject(text)
    }

    private fun required(payload: JSONObject, vararg names: String): String =
        names.asSequence().mapNotNull { optionalValue(payload, it) }.firstOrNull()
            ?: throw IllegalStateException("Telemetry pairing response omitted its id")

    private fun optional(payload: JSONObject, vararg names: String): String? =
        names.asSequence().mapNotNull { optionalValue(payload, it) }.firstOrNull()

    private fun optionalValue(payload: JSONObject, name: String): String? =
        payload.optString(name).takeIf { it.isNotBlank() && it != "null" }
}
