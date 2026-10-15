package cyou.rime.telemetry.collector

import org.json.JSONObject
import java.time.Duration
import java.time.Instant

/** What the Now screen shows, derived only from server state or its cached timeline. */
data class NowView(
    val mode: String,
    val modeLabel: String,
    val reason: String,
    val taskTitle: String?,
    val nextAction: String?,
    val resumeMarker: String?,
    val taskState: String?,
    val planExists: Boolean,
    val asOf: Instant,
    val offline: Boolean,
)

object NowModel {
    private val modeLabels = mapOf(
        "emergency" to "Emergency unlock",
        "gate" to "Locked for now",
        "urgent" to "Urgent task",
        "recovery" to "Offline recovery",
        "work" to "Working",
        "break" to "Break",
        "plan_gate" to "Plan your day",
        "outside" to "Go outside first",
        "closed" to "Focus hours",
        "leisure" to "Free time",
    )

    private val stateLabels = mapOf(
        "active" to "In progress",
        "waiting_external" to "Waiting on an agent or build",
        "interrupted" to "Paused",
        "offline_recovery" to "Offline recovery",
        "not_started" to "Not started yet",
    )

    fun modeLabel(mode: String): String = modeLabels[mode] ?: "Unknown"

    fun stateLabel(state: String?): String? = state?.let { stateLabels[it] ?: it }

    /**
     * Uses the live decision when the response is fresh. When offline, it
     * follows the cached timeline segment covering [now], so the phone keeps
     * showing the approved policy instead of guessing.
     */
    fun parse(json: String, fetchedAt: Instant, now: Instant, offline: Boolean): NowView {
        val root = JSONObject(json)
        var decision = root.getJSONObject("decision")
        if (offline) {
            val timeline = root.optJSONArray("timeline")
            if (timeline != null) {
                for (index in 0 until timeline.length()) {
                    val segment = timeline.getJSONObject(index)
                    val from = Instant.parse(segment.getString("from"))
                    val to = Instant.parse(segment.getString("to"))
                    if (!now.isBefore(from) && now.isBefore(to)) {
                        decision = segment.getJSONObject("decision")
                        break
                    }
                }
            }
        }
        val task = root.optJSONObject("task")
        return NowView(
            mode = decision.optString("mode"),
            modeLabel = modeLabel(decision.optString("mode")),
            reason = decision.optString("reason"),
            taskTitle = task?.optString("title")?.ifBlank { null },
            nextAction = task?.optString("next_action")?.ifBlank { null },
            resumeMarker = task?.optString("resume_marker")?.ifBlank { null },
            taskState = stateLabel(task?.optString("state")?.ifBlank { null }),
            planExists = root.optBoolean("plan_exists"),
            asOf = fetchedAt,
            offline = offline,
        )
    }

    /** Past the cached timeline the phone no longer claims to know the policy. */
    fun timelineCovers(json: String, now: Instant): Boolean {
        val timeline = JSONObject(json).optJSONArray("timeline") ?: return false
        if (timeline.length() == 0) return false
        val last = timeline.getJSONObject(timeline.length() - 1)
        return now.isBefore(Instant.parse(last.getString("to")))
    }

    fun ageText(asOf: Instant, now: Instant): String {
        val minutes = Duration.between(asOf, now).toMinutes()
        return when {
            minutes < 1 -> "just now"
            minutes < 60 -> "$minutes min ago"
            else -> "${minutes / 60} h ago"
        }
    }
}
