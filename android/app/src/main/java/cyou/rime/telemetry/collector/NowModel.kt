package cyou.rime.telemetry.collector

import java.time.Duration
import java.time.Instant

/** Names for the server's decision modes, and how old something is, in words. */
object NowModel {
    private val modeLabels = mapOf(
        "emergency" to "Emergency unlock",
        "door" to "Opened through the door",
        "budget_used" to "Distraction time used up",
        "bedtime" to "Bedtime",
        "morning" to "Morning",
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

    fun modeLabel(mode: String): String = modeLabels[mode] ?: "Unknown"

    fun ageText(asOf: Instant, now: Instant): String {
        val minutes = Duration.between(asOf, now).toMinutes()
        return when {
            minutes < 1 -> "just now"
            minutes < 60 -> "$minutes min ago"
            else -> "${minutes / 60} h ago"
        }
    }
}
