package cyou.rime.telemetry.collector

import android.content.Context
import java.time.Instant

/** The last /v1/device/now response, shared by blocking, the watcher and the app while it is open. */
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
        val body = DeviceApi.get(context, "/v1/device/now?places=1") ?: return null
        store(context, body)
        return body
    }

    /** Keeps an answer that arrived some other way, such as a waited-for change. */
    fun store(context: Context, body: String) {
        adoptRelay(context, body)
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(KEY_JSON, body)
            .putString(KEY_FETCHED_AT, Instant.now().toString())
            .commit()
        HabitWidgets.changed(context)
    }

    /**
     * Puts a new `budget` (an offline door answer, say) into the cached
     * answer, leaving its timeline and when it was fetched as they were.
     */
    fun patchBudget(context: Context, budget: org.json.JSONObject) {
        val entry = read(context) ?: return
        val json = runCatching { org.json.JSONObject(entry.json).put("budget", budget).toString() }.getOrNull() ?: return
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_JSON, json).commit()
        HabitWidgets.changed(context)
    }

    /** The budget in the cached answer, or null. */
    fun budget(context: Context): BudgetView? = read(context)?.json?.let(Budget::parse)

    /** The server's fingerprint of the cached answer, or null. */
    fun fingerprint(context: Context): String? =
        read(context)?.json?.let { runCatching { org.json.JSONObject(it).optString("fingerprint") }.getOrNull() }?.takeIf { it.isNotBlank() }

    /**
     * Saves the relay the server names, so a phone set up without it reaches
     * the server without Tailscale from then on (2026-11-22).
     */
    private fun adoptRelay(context: Context, body: String) {
        val relay = runCatching { org.json.JSONObject(body).optJSONObject("relay") }.getOrNull() ?: return
        val url = relay.optString("url").trim().trimEnd('/')
        val key = relay.optString("key").trim()
        val config = DeliveryConfig(context)
        if (!RelayAddress.acceptable(url, key) || (url == config.relayUrl && key == config.relayKey)) return
        if (runCatching { SealedChannel(url, key) }.isFailure) return
        config.relayUrl = url
        config.relayKey = key
    }
}

/** A relay address worth saving: https, with a key. */
internal object RelayAddress {
    fun acceptable(url: String, key: String): Boolean =
        url.startsWith("https://") && url.length > "https://".length && !url.contains(' ') && key.isNotBlank()
}
