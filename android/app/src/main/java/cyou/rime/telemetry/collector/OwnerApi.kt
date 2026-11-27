package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.time.Duration
import java.time.Instant

/** One answer from the server: its status, type and body as bytes. */
class RawAnswer(val status: Int, val contentType: String, val body: ByteArray) {
    val text: String get() = String(body, Charsets.UTF_8)
    val ok: Boolean get() = status in 200..299

    /** The server's own words for a refusal ({"error":{"detail":…}}), else the status. */
    fun message(): String = refusalMessage(status, text)

    companion object {
        fun refusalMessage(status: Int, body: String): String {
            val json = runCatching { JSONObject(body) }.getOrNull()
            val error = json?.opt("error")
            val words = when (error) {
                is JSONObject -> error.optString("detail").ifBlank { error.optString("message") }
                is String -> json.optString("message").ifBlank { error }
                else -> json?.optString("message").orEmpty()
            }
            return words.trim().ifBlank { "The server refused it (it answered $status)." }
        }
    }
}

/**
 * The panel session a paired phone trades its device token for
 * (POST /v1/device/panel-session), kept in memory and traded again before
 * it ends or when the server stops taking it (a 401). Owner routes accept
 * it (ownerAuthorized includes device sessions) and the relay carries
 * owner paths when it is present.
 */
class OwnerSession(
    /** Trades the device token for a session. Blocking IO. */
    private val trade: () -> Trade,
    private val now: () -> Instant = Instant::now,
) {
    sealed interface Trade {
        data class Opened(val cookie: String, val expiresAt: Instant?) : Trade
        /** The server answered without a session. */
        data class Refused(val answer: RawAnswer) : Trade
        data object Unreachable : Trade
    }

    private var cookie: String? = null
    private var openedAt: Instant = Instant.EPOCH
    private var expiresAt: Instant? = null

    /** How many times a session was traded for; for tests. */
    var trades = 0
        private set

    /**
     * A session good for now: the kept one, or a new one when there is none
     * or the kept one is near its end. A failed renewal keeps the old one
     * while it is still valid.
     */
    @Synchronized
    fun current(): Trade {
        val kept = cookie
        val at = now()
        if (kept != null && !renewDue(expiresAt, openedAt, at)) return Trade.Opened(kept, expiresAt)
        val traded = trade()
        trades++
        if (traded is Trade.Opened) {
            cookie = traded.cookie
            openedAt = at
            expiresAt = traded.expiresAt
            return traded
        }
        val stillValid = kept != null && expiresAt?.let { at.isBefore(it) } == true
        return if (stillValid) Trade.Opened(kept!!, expiresAt) else traded
    }

    /** Forgets [used], so the next call trades again. */
    @Synchronized
    fun drop(used: String) {
        if (cookie == used) cookie = null
    }

    /**
     * Sends one request with the session. A 401 trades once more and sends
     * again. Null when the server can't be reached; a refused trade gives
     * the server's answer to it.
     */
    fun call(send: (cookie: String) -> RawAnswer?): RawAnswer? {
        val first = when (val session = current()) {
            is Trade.Opened -> session.cookie
            is Trade.Refused -> return session.answer
            Trade.Unreachable -> return null
        }
        val answer = send(first) ?: return null
        if (answer.status != 401) return answer
        drop(first)
        val again = (current() as? Trade.Opened)?.cookie ?: return answer
        return send(again)
    }

    companion object {
        /** Renewed this long before it runs out. */
        private val MARGIN: Duration = Duration.ofHours(1)

        fun parse(body: String): Trade.Opened? = runCatching {
            val json = JSONObject(body)
            val cookie = json.getString("cookie").takeIf { it.isNotBlank() } ?: return null
            Trade.Opened(cookie, json.optString("expires_at").takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() })
        }.getOrNull()

        /** True when the session should be traded again: near its end, or an hour old when its end is unknown. */
        fun renewDue(expiresAt: Instant?, openedAt: Instant, now: Instant): Boolean =
            if (expiresAt != null) !now.isBefore(expiresAt.minus(MARGIN)) else !now.isBefore(openedAt.plus(MARGIN))
    }
}

/**
 * The owner's routes, reached as the panel reaches them, with this phone's
 * panel session: through the sealed relay first, then the server's own
 * address. Blocking IO throughout.
 */
object OwnerApi {
    @Volatile private var session: OwnerSession? = null

    private fun session(context: Context): OwnerSession = session ?: synchronized(this) {
        session ?: OwnerSession({ trade(context.applicationContext) }).also { session = it }
    }

    private fun trade(context: Context): OwnerSession.Trade {
        val answer = DeviceApi.send(context, "POST", "/v1/device/panel-session") ?: return OwnerSession.Trade.Unreachable
        if (answer.status >= 500) return OwnerSession.Trade.Unreachable
        if (answer.status != 200) return OwnerSession.Trade.Refused(RawAnswer(answer.status, "application/json", answer.body.toByteArray()))
        return OwnerSession.parse(answer.body) ?: OwnerSession.Trade.Unreachable
    }

    /** One request to an owner route; null when the server can't be reached or the phone isn't paired. */
    fun send(context: Context, method: String, path: String, body: String? = null, accept: String = "application/json", readTimeoutMillis: Int = 30_000): RawAnswer? {
        if (DeliveryConfig(context).token() == null) return null
        return session(context).call { cookie -> exchange(context, method, path, body, cookie, accept, readTimeoutMillis) }
    }

    fun get(context: Context, path: String): RawAnswer? = send(context, "GET", path)

    private fun exchange(context: Context, method: String, path: String, body: String?, cookie: String, accept: String, readTimeoutMillis: Int): RawAnswer? {
        val headers = mutableMapOf("Cookie" to cookie, "Accept" to accept)
        if (body != null) headers["Content-Type"] = "application/json"
        SealedChannel.configured(context)?.let { channel ->
            val answer = try {
                channel.exchange(method, path, headers, body?.toByteArray(), 10_000, readTimeoutMillis)
            } catch (_: java.io.IOException) {
                null
            }
            // A relay that answers for a server it can't reach.
            if (answer != null && answer.status !in 502..504) return RawAnswer(answer.status, answer.contentType, answer.body)
        }
        val server = DeliveryConfig(context).serverUrl.trimEnd('/')
        if (server.isEmpty()) return null
        return runCatching {
            val connection = URL(server + path).openConnection() as HttpURLConnection
            try {
                connection.requestMethod = method
                connection.connectTimeout = 10_000
                connection.readTimeout = readTimeoutMillis
                connection.instanceFollowRedirects = false
                headers.forEach { (name, value) -> connection.setRequestProperty(name, value) }
                if (body != null) {
                    connection.doOutput = true
                    connection.outputStream.use { it.write(body.toByteArray()) }
                }
                val status = connection.responseCode
                val stream = if (status in 200..399) connection.inputStream else connection.errorStream
                RawAnswer(status, connection.contentType.orEmpty(), stream?.use { it.readBytes() } ?: ByteArray(0))
            } finally {
                connection.disconnect()
            }
        }.getOrNull()?.takeIf { it.status !in 502..504 }
    }
}
