package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URL
import java.security.SecureRandom
import java.time.Instant
import java.util.concurrent.Executors

/**
 * Shows the panel through the sealed relay, so the app works without
 * Tailscale like the rest of the phone does (owner 2026-11-26: the app sat on
 * "Opening Telemetry…" and then "Can't reach your Telemetry server" with
 * Tailscale off). The WebView loads the panel from this small server on the
 * phone's loopback. Each request goes through the relay first and straight to
 * the server's address only when the relay can't be reached, carrying the
 * panel session this phone traded its token for. The WebView never holds that
 * session. Other apps on the phone can reach the loopback too, so every
 * request must carry a random key the WebView alone has, as a cookie.
 */
class PanelProxy(private val context: Context) {
    companion object {
        const val KEY_COOKIE = "telemetry_app"
        private const val MAX_BODY = 8 shl 20

        /** The panel session from /v1/device/panel-session's answer. */
        fun parseSession(body: String): Pair<String, Instant?>? = runCatching {
            val json = JSONObject(body)
            val cookie = json.getString("cookie").takeIf { it.isNotBlank() } ?: return null
            cookie to json.optString("expires_at").takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() }
        }.getOrNull()

        /** Whether a Cookie header carries this key. */
        fun keyed(cookieHeader: String?, key: String): Boolean = cookieHeader.orEmpty().split(';').any {
            val (name, value) = it.trim().split('=', limit = 2).let { parts -> parts[0] to parts.getOrElse(1) { "" } }
            name == KEY_COOKIE && value == key
        }
    }

    /** What trading the token for a panel session gave. */
    sealed interface Opened {
        data object Yes : Opened
        data class Refused(val status: Int) : Opened
        data object Unreachable : Opened
    }

    val key: String = ByteArray(24).also(SecureRandom()::nextBytes).joinToString("") { "%02x".format(it) }
    private val socket = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    val origin: String = "http://127.0.0.1:${socket.localPort}"
    private val workers = Executors.newFixedThreadPool(6)

    @Volatile private var session: String? = null
    @Volatile private var openedAt: Instant = Instant.EPOCH
    @Volatile private var expiresAt: Instant? = null

    init {
        Thread({
            while (!socket.isClosed) {
                val client = runCatching { socket.accept() }.getOrNull() ?: continue
                workers.execute { runCatching { serve(client) }; runCatching { client.close() } }
            }
        }, "panel-proxy").apply { isDaemon = true }.start()
    }

    fun close() {
        runCatching { socket.close() }
        workers.shutdownNow()
    }

    /** Trades this phone's token for a panel session, relay first. Blocking IO. */
    fun open(): Opened {
        val answer = DeviceApi.send(context, "POST", "/v1/device/panel-session") ?: return Opened.Unreachable
        if (answer.status != 200) return if (answer.status >= 500) Opened.Unreachable else Opened.Refused(answer.status)
        val (cookie, expires) = parseSession(answer.body) ?: return Opened.Unreachable
        session = cookie
        openedAt = Instant.now()
        expiresAt = expires
        return Opened.Yes
    }

    /** A new session before the old one ends. Blocking IO. */
    fun renewIfDue(now: Instant = Instant.now()) {
        if (session == null || PanelSession.renewDue(expiresAt, openedAt, now)) open()
    }

    private class Request(val method: String, val path: String, val headers: Map<String, String>, val body: ByteArray?)

    private fun serve(client: Socket) {
        client.soTimeout = 30_000
        val input = BufferedInputStream(client.getInputStream())
        val request = read(input) ?: return
        val output = client.getOutputStream()
        if (!keyed(request.headers["cookie"], key)) {
            respond(output, 403, "text/plain", "Forbidden".toByteArray())
            return
        }
        var answer = forward(request)
        // The session ran out while the page was open: trade again once.
        if (answer != null && answer.status == 401 && open() == Opened.Yes) answer = forward(request)
        if (answer == null) {
            respond(output, 502, "text/plain", "The Telemetry server can't be reached.".toByteArray())
            return
        }
        respond(output, answer.status, answer.contentType, answer.body)
    }

    private class Answer(val status: Int, val contentType: String, val body: ByteArray)

    /** Relay first, then the server's own address; null when neither answers. */
    private fun forward(request: Request): Answer? {
        val headers = mutableMapOf<String, String>()
        session?.let { headers["Cookie"] = it }
        request.headers["content-type"]?.let { headers["Content-Type"] = it }
        headers["Accept"] = request.headers["accept"] ?: "*/*"
        SealedChannel.configured(context)?.let { channel ->
            val sealed = try {
                channel.exchange(request.method, request.path, headers, request.body, 10_000, 30_000)
            } catch (_: java.io.IOException) {
                null
            }
            if (sealed != null) return Answer(sealed.status, sealed.contentType, sealed.body)
        }
        val server = DeliveryConfig(context).serverUrl.trimEnd('/')
        if (server.isEmpty()) return null
        return runCatching {
            val connection = URL(server + request.path).openConnection() as HttpURLConnection
            try {
                connection.requestMethod = request.method
                connection.connectTimeout = 10_000
                connection.readTimeout = 30_000
                connection.instanceFollowRedirects = false
                headers.forEach { (name, value) -> connection.setRequestProperty(name, value) }
                if (request.body != null && request.body.isNotEmpty()) {
                    connection.doOutput = true
                    connection.outputStream.use { it.write(request.body) }
                }
                val status = connection.responseCode
                val stream = if (status in 200..399) connection.inputStream else connection.errorStream
                Answer(status, connection.contentType.orEmpty(), stream?.use { it.readBytes() } ?: ByteArray(0))
            } finally {
                connection.disconnect()
            }
        }.getOrNull()
    }

    private fun read(input: InputStream): Request? {
        val line = readLine(input) ?: return null
        val parts = line.split(' ')
        if (parts.size < 2) return null
        val headers = mutableMapOf<String, String>()
        while (true) {
            val header = readLine(input) ?: return null
            if (header.isEmpty()) break
            val colon = header.indexOf(':')
            if (colon > 0) headers[header.substring(0, colon).trim().lowercase()] = header.substring(colon + 1).trim()
        }
        val length = headers["content-length"]?.toIntOrNull() ?: 0
        if (length < 0 || length > MAX_BODY) return null
        val body = if (length > 0) ByteArray(length).also { buffer ->
            var read = 0
            while (read < length) {
                val n = input.read(buffer, read, length - read)
                if (n < 0) return null
                read += n
            }
        } else null
        // Only the panel and its API, never another host.
        val path = parts[1]
        if (!path.startsWith("/")) return null
        return Request(parts[0].uppercase(), path, headers, body)
    }

    private fun readLine(input: InputStream): String? {
        val bytes = ByteArrayOutputStream()
        while (true) {
            val b = input.read()
            if (b < 0) return if (bytes.size() == 0) null else bytes.toString(Charsets.UTF_8.name())
            if (b == '\n'.code) return bytes.toString(Charsets.UTF_8.name()).trimEnd('\r')
            if (bytes.size() > 16 shl 10) return null
            bytes.write(b)
        }
    }

    private fun respond(output: java.io.OutputStream, status: Int, contentType: String, body: ByteArray) {
        val head = buildString {
            append("HTTP/1.1 ").append(status).append(' ').append(if (status in 200..299) "OK" else "Status").append("\r\n")
            if (contentType.isNotEmpty()) append("Content-Type: ").append(contentType).append("\r\n")
            append("Content-Length: ").append(body.size).append("\r\n")
            append("Cache-Control: no-store\r\n")
            append("Connection: close\r\n\r\n")
        }
        output.write(head.toByteArray(Charsets.US_ASCII))
        output.write(body)
        output.flush()
    }
}
