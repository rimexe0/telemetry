package cyou.rime.telemetry.collector

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.time.Duration
import java.time.Instant

/** A part of [SettingsActivity] the panel can open directly. */
enum class SettingsSection(val key: String) {
    PERMISSIONS("permissions"),
    NOTIFICATIONS("notifications");

    companion object {
        fun of(key: String?): SettingsSection? = entries.firstOrNull { it.key == key }
    }
}

/** Where a link in the panel leads. */
sealed interface PanelRoute {
    /** A page of the owner's server: stays in the app. */
    data object InPanel : PanelRoute

    /** Another site: opens in the phone's browser, where blocking applies. */
    data class Browser(val url: String) : PanelRoute

    data class Settings(val section: SettingsSection?) : PanelRoute

    data class Setup(val step: SetupStep?) : PanelRoute

    /** Not followed at all. */
    data object Refused : PanelRoute
}

/**
 * The app's Activity and Settings are the panel in a web view. The panel
 * reaches what must stay on the phone through telemetry-app:// links:
 *
 * - telemetry-app://settings/permissions and telemetry-app://settings/notifications
 *   open those parts of [SettingsActivity] (telemetry-app://settings opens it at the top);
 * - telemetry-app://setup opens [SetupActivity], at ?step=<SetupStep name> when given.
 *
 * Any other link that is not http(s) is never loaded.
 */
object PanelLinks {
    const val SCHEME = "telemetry-app"

    /**
     * Lets the panel tell it is inside the app, and which version, and that
     * the app draws the places bar itself, so the panel leaves its own out.
     */
    fun userAgent(default: String, versionName: String): String = "$default TelemetryApp/$versionName TelemetryAppNav/1"

    /**
     * Which of the app's places a panel address belongs to, as the panel's
     * nav.ts reads its addresses: Activity's sections and the old day, plan
     * and health links; Settings' pages and the habit editing; Today otherwise.
     */
    fun place(hash: String): String = when (hash.removePrefix("#").substringBefore('/')) {
        "activity", "day", "plan", "health" -> "activity"
        "settings", "rules", "collection", "devices", "sign-in", "habits" -> "settings"
        else -> "today"
    }

    /** A panel address safe to put in a script: only the characters the panel's addresses use. */
    fun safeHash(hash: String): String? = hash.takeIf { Regex("#[A-Za-z0-9/_-]{0,64}").matches(it) }

    fun route(url: String, server: String, mainFrame: Boolean = true): PanelRoute {
        val uri = runCatching { URI(url.trim()) }.getOrNull() ?: return PanelRoute.Refused
        return when (uri.scheme?.lowercase()) {
            SCHEME -> app(uri)
            "http", "https" -> when {
                sameOrigin(uri, server) -> PanelRoute.InPanel
                // Only a page the owner opens leaves the app; what a page embeds stays.
                mainFrame -> PanelRoute.Browser(url)
                else -> PanelRoute.InPanel
            }
            else -> PanelRoute.Refused
        }
    }

    private fun app(uri: URI): PanelRoute {
        val path = uri.path.orEmpty().trim('/')
        return when (uri.host?.lowercase()) {
            "settings" -> if (path.isEmpty()) PanelRoute.Settings(null) else SettingsSection.of(path)?.let { PanelRoute.Settings(it) } ?: PanelRoute.Refused
            "setup" -> if (path.isNotEmpty()) PanelRoute.Refused else PanelRoute.Setup(step(uri.rawQuery))
            else -> PanelRoute.Refused
        }
    }

    private fun step(query: String?): SetupStep? {
        val name = query?.split('&')?.firstOrNull { it.startsWith("step=") }?.removePrefix("step=") ?: return null
        return SetupStep.entries.firstOrNull { it.name.equals(name, ignoreCase = true) }
    }

    private fun sameOrigin(uri: URI, server: String): Boolean {
        val home = runCatching { URI(server) }.getOrNull() ?: return false
        fun port(u: URI) = if (u.port != -1) u.port else if (u.scheme.equals("https", true)) 443 else 80
        return uri.scheme.equals(home.scheme, true) && uri.host != null && uri.host.equals(home.host, true) && port(uri) == port(home)
    }
}

/**
 * The paired device credential traded for a short-lived panel session, so the
 * app needs no separate login.
 */
object PanelSession {
    sealed interface Result {
        data class Opened(val cookie: String, val expiresAt: Instant?) : Result
        /** The server answered but did not open a session. */
        data class Refused(val status: Int) : Result
        data object Unreachable : Result
    }

    /** Renewed this long before it runs out, so an open panel never loses it. */
    private val MARGIN: Duration = Duration.ofHours(1)

    /** Blocking IO. */
    fun request(server: String, deviceId: String, token: String): Result {
        val connection = try {
            URL("$server/v1/device/panel-session").openConnection() as HttpURLConnection
        } catch (_: Exception) {
            return Result.Unreachable
        }
        return try {
            connection.requestMethod = "POST"
            connection.connectTimeout = 10_000
            connection.readTimeout = 10_000
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("X-Telemetry-Device-ID", deviceId)
            val status = connection.responseCode
            if (status != 200) return if (status >= 500) Result.Unreachable else Result.Refused(status)
            parse(connection.inputStream.bufferedReader().use { it.readText() }) ?: Result.Unreachable
        } catch (_: java.io.IOException) {
            Result.Unreachable
        } finally {
            connection.disconnect()
        }
    }

    fun parse(body: String): Result.Opened? = runCatching {
        val json = JSONObject(body)
        val cookie = json.getString("cookie").takeIf { it.isNotBlank() } ?: return null
        Result.Opened(cookie, json.optString("expires_at").takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() })
    }.getOrNull()

    /** True when the session should be traded again: near its end, or an hour old when its end is unknown. */
    fun renewDue(expiresAt: Instant?, openedAt: Instant, now: Instant): Boolean =
        if (expiresAt != null) !now.isBefore(expiresAt.minus(MARGIN)) else !now.isBefore(openedAt.plus(MARGIN))
}
