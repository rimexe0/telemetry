package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant

/** What the approved policy blocks at one moment, taken from the cached timeline. */
data class BlockDecision(
    val mode: String,
    val reason: String,
    val policyVersion: Long,
    val blockAll: Boolean,
    val blocked: Set<String>,
    val alwaysAllowed: Set<String>,
    /** The wind-down before bedtime: the screen turns grayscale. */
    val grayscale: Boolean = false,
) {
    val active: Boolean get() = blockAll || blocked.isNotEmpty()
}

object BlockPolicy {
    private const val CLOCK_TOLERANCE_SECONDS = 600L

    /**
     * The decision covering [now], or null when the cache does not cover it.
     * A missing or out-of-date cache never blocks anything.
     */
    fun at(json: String, now: Instant): BlockDecision? {
        val timeline = JSONObject(json).optJSONArray("timeline") ?: return null
        if (timeline.length() == 0) return null
        // Tolerate a device clock running a little behind the server's.
        val first = timeline.getJSONObject(0)
        val firstFrom = Instant.parse(first.getString("from"))
        if (now.isBefore(firstFrom) && !now.isBefore(firstFrom.minusSeconds(CLOCK_TOLERANCE_SECONDS))) {
            return parse(first.getJSONObject("decision"))
        }
        for (index in 0 until timeline.length()) {
            val segment = timeline.getJSONObject(index)
            if (!now.isBefore(Instant.parse(segment.getString("from"))) && now.isBefore(Instant.parse(segment.getString("to")))) {
                return parse(segment.getJSONObject("decision"))
            }
        }
        return null
    }

    fun parse(decision: JSONObject): BlockDecision = BlockDecision(
        mode = decision.optString("mode"),
        reason = decision.optString("reason"),
        policyVersion = decision.optLong("policy_version"),
        blockAll = decision.optBoolean("block_all"),
        blocked = strings(decision.optJSONObject("blocked")?.optJSONArray("android_packages")),
        alwaysAllowed = strings(decision.optJSONObject("always_allowed")?.optJSONArray("android_packages")),
        grayscale = decision.optBoolean("grayscale"),
    )

    /**
     * Whether [pkg] should be blocked. The owner's essentials, this app and
     * [systemSafe] (launcher, system UI, settings, dialer) are never blocked.
     */
    fun shouldBlock(decision: BlockDecision?, pkg: String, systemSafe: Set<String>): Boolean {
        if (decision == null || pkg.isBlank() || pkg in decision.alwaysAllowed || pkg in systemSafe) return false
        return decision.blockAll || pkg in decision.blocked
    }

    private fun strings(array: JSONArray?): Set<String> {
        if (array == null) return emptySet()
        return (0 until array.length()).map { array.getString(it) }.toSet()
    }
}
