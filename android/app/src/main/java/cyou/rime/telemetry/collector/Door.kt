package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONObject

/**
 * The door (2026-11-26): the one way to open distractions at more than 1×,
 * which replaced the free emergency unlock. Everything here asks the server;
 * without it there is no door, and essentials stay open as always.
 */
object Door {
    /** How a call to the server went. */
    sealed interface Outcome {
        /** Accepted; [body] is the server's answer. */
        data class Done(val body: JSONObject) : Outcome
        /** Refused (409 and the like), with the server's own words. */
        data class Refused(val message: String) : Outcome
        /** The server could not be reached. */
        data object Unreachable : Outcome
    }

    const val UNREACHABLE = "The server can't be reached, so no door can open now. Calls, maps and the other essentials stay open."

    /** "Still want it": creates a door that opens after the wait. Blocking IO. */
    fun request(context: Context): Outcome = call(context, "/v1/device/door", "{}").also { outcome ->
        if (outcome is Outcome.Done) adopt(context) { it.put("door", outcome.body) }
    }

    /** Cancels a door while it waits, at no cost. Blocking IO. */
    fun cancel(context: Context, id: Long): Outcome = call(context, "/v1/device/door/$id/cancel", "{}").also { outcome ->
        if (outcome is Outcome.Done) adopt(context) { it.put("door", JSONObject.NULL) }
    }

    /**
     * Back on the door screen: a door declined, which counts as returning.
     * When the server cannot be reached it is kept as an observation instead,
     * so the return is not lost. Blocking IO.
     */
    fun back(context: Context, decision: BlockDecision?, pkg: String) {
        if (call(context, "/v1/device/door/back", "{}") is Outcome.Done) return
        val now = java.time.Instant.now()
        PolicyEvents.record(context, "android.policy_door_back", "door-back:${now.toEpochMilli()}", decision, mapOf("package" to pkg, "at" to now.toString()))
    }

    /** Reports grayscale kept off ("decline") or turned back on ("restore"). Blocking IO. */
    fun grayscale(context: Context, action: String): Outcome =
        call(context, "/v1/device/grayscale", JSONObject().put("action", action).put("platform", "android").toString())

    fun outcome(answer: DeviceApi.Answer?): Outcome = when {
        answer == null -> Outcome.Unreachable
        answer.status in 200..299 -> Outcome.Done(runCatching { JSONObject(answer.body) }.getOrElse { JSONObject() })
        // A relay that answers but cannot reach the server is no refusal.
        answer.status >= 500 -> Outcome.Unreachable
        else -> Outcome.Refused(Budget.refusal(answer.body) ?: "The server refused this (${answer.status}).")
    }

    private fun call(context: Context, path: String, body: String): Outcome = outcome(DeviceApi.send(context, "POST", path, body))

    /**
     * Fetches the new state with its timeline; offline, the answer just given
     * is put into the cached budget so the screen shows it.
     */
    private fun adopt(context: Context, patch: (JSONObject) -> Unit) {
        if (NowCache.refresh(context) != null) return
        val cached = NowCache.read(context)?.json?.let { runCatching { JSONObject(it).optJSONObject("budget") }.getOrNull() } ?: return
        patch(cached)
        NowCache.patchBudget(context, cached)
    }
}
