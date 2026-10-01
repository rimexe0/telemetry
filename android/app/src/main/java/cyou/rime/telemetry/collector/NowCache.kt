package cyou.rime.telemetry.collector

import android.content.Context
import java.time.Instant

/** The last /v1/device/now response, shared by the Now screen, blocking and the watcher. */
object NowCache {
    private const val PREFS = "telemetry-now"
    private const val KEY_JSON = "json"
    private const val KEY_FETCHED_AT = "fetched_at"

    data class Entry(val json: String, val fetchedAt: Instant)

    fun read(context: Context): Entry? {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val json = preferences.getString(KEY_JSON, null) ?: return null
        val fetchedAt = preferences.getString(KEY_FETCHED_AT, null)?.let(Instant::parse) ?: return null
        return Entry(json, fetchedAt)
    }

    /** Fetches and stores a fresh response; returns null when offline or unpaired. Blocking IO. */
    fun refresh(context: Context): String? {
        val body = DeviceApi.get(context, "/v1/device/now") ?: return null
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(KEY_JSON, body)
            .putString(KEY_FETCHED_AT, Instant.now().toString())
            .apply()
        return body
    }
}
