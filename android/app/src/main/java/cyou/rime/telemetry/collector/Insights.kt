package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONObject
import java.time.Instant

/** One short fact about today, computed by the server from every device. */
data class Insight(val key: String, val text: String, val detail: String?)

/** Today's facts for the home screen; the last answer is kept for offline. */
object Insights {
    private const val PREFS = "telemetry-insights"
    private const val KEY_JSON = "json"
    private const val KEY_FETCHED_AT = "fetched_at"

    data class Entry(val json: String, val fetchedAt: Instant)

    /**
     * Reads the server's answer. Each insight lists the raw app identifiers
     * in its text; [appName] gives this phone's name for one, or null.
     */
    fun parse(json: String, appName: (String) -> String?): List<Insight> {
        val items = JSONObject(json).optJSONArray("insights") ?: return emptyList()
        return (0 until items.length()).map { index ->
            val item = items.getJSONObject(index)
            var text = item.optString("text")
            var detail = item.optString("detail")
            val names = item.optJSONArray("names")
            if (names != null) {
                for (position in 0 until names.length()) {
                    val raw = names.getString(position)
                    val shown = appName(raw) ?: continue
                    text = text.replace(raw, shown)
                    detail = detail.replace(raw, shown)
                }
            }
            Insight(item.optString("key"), text, detail.ifBlank { null })
        }
    }

    /** Whether the answer is for the owner's current day; the server names the day. */
    fun isToday(json: String, now: Instant, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): Boolean =
        runCatching { JSONObject(json).optString("date") == now.atZone(zone).toLocalDate().toString() }.getOrDefault(false)

    /** The app's own name for an installed package; null for anything else. */
    fun appName(context: Context): (String) -> String? = { raw ->
        runCatching {
            val manager = context.packageManager
            manager.getApplicationLabel(manager.getApplicationInfo(raw, 0)).toString()
        }.getOrNull()
    }

    fun read(context: Context): Entry? {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val json = preferences.getString(KEY_JSON, null) ?: return null
        val fetchedAt = preferences.getString(KEY_FETCHED_AT, null)?.let(Instant::parse) ?: return null
        return Entry(json, fetchedAt)
    }

    /** Fetches and stores today's facts; null when offline or unpaired. Blocking IO. */
    fun refresh(context: Context): String? {
        val body = DeviceApi.get(context, "/v1/device/insights") ?: return null
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(KEY_JSON, body)
            .putString(KEY_FETCHED_AT, Instant.now().toString())
            .apply()
        return body
    }
}
