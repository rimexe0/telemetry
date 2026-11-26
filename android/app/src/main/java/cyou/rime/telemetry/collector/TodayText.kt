package cyou.rime.telemetry.collector

import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import kotlin.math.ceil

/** "Right now" on Today: a line, when it started, and one note. */
data class RightNow(val line: String, val since: String, val note: String?)

/**
 * The words on the phone's Today, as the panel's Today words them
 * (web/src/lib/today.ts and budget.ts). They state facts, never scold, and
 * say "unknown" where nothing was recorded.
 */
object TodayText {
    /** What the server reads at the Mac, from a /v1/device/now body's `tracking`. */
    fun rightNow(json: String?, zone: ZoneId): RightNow? {
        val tracking = json?.let { runCatching { JSONObject(it).optJSONObject("tracking") }.getOrNull() } ?: return null
        val task = tracking.optString("task")
        val context = tracking.optString("context")
        val state = tracking.optString("state")
        var line = when (state) {
            "task" -> if (task.isNotBlank()) "On $task" else "On an open task"
            "distraction" -> if (context.isNotBlank()) "On a distraction: $context" else "On a distraction"
            "other" -> if (context.isNotBlank()) "On $context" else "On something outside the open tasks"
            "away" -> "Nothing recent from the Mac"
            else -> "Unknown"
        }
        if (tracking.optBoolean("excused")) line += " (a one-off you mentioned)"
        val notes = mutableListOf<String>()
        tracking.optJSONObject("focus")?.let { focus ->
            val offSince = instant(focus.optString("off_since"))
            val stand = when (focus.optString("on")) {
                "on" -> "on it"
                "away" -> "away from the Mac"
                "off" -> "away from it" + (offSince?.let { " since ${BudgetText.clock(it, zone)}" } ?: "")
                else -> "how it stands is unknown"
            }
            val next = focus.optString("next_action").takeIf { it.isNotBlank() }?.let { " Next: $it" } ?: ""
            notes += "Focus: ${focus.optString("task")}, $stand.$next"
        }
        tracking.optJSONArray("your_turn")?.let { turns ->
            for (index in 0 until turns.length()) {
                val turn = turns.optJSONObject(index) ?: continue
                val at = instant(turn.optString("since"))?.let { BudgetText.clock(it, zone) } ?: "an unknown time"
                notes += "Your turn on ${turn.optString("task")}: its agent stopped at $at."
            }
        }
        if (tracking.optBoolean("agent_running")) notes += "An agent is at work."
        val since = if (state == "away") "" else instant(tracking.optString("since"))?.let { BudgetText.clock(it, zone) }.orEmpty()
        return RightNow(line, since, notes.firstOrNull())
    }

    /**
     * The muted line under the time left: the rate now, then the distraction
     * time it is in or when the next starts, then what habits earned.
     * "3× now · distraction time from 19:00 · +30 min earned".
     */
    fun budgetLine(budget: BudgetView, zone: ZoneId): String {
        val parts = mutableListOf<String>()
        val rate = budget.rate
        parts += if (rate.grayscaleOff > 0) "${BudgetText.number(rate.total)}× now (${BudgetText.number(rate.grayscaleOff)}× with grayscale kept off)" else "${BudgetText.number(rate.total)}× now"
        when {
            rate.kind == "bedtime" -> parts += "bedtime"
            budget.inDistractionTime -> parts += "distraction time"
            budget.nextDistractionTime != null -> parts += "distraction time from ${BudgetText.clock(budget.nextDistractionTime, zone)}"
        }
        if (budget.earned > 0) parts += "+${BudgetText.minutes(budget.earned)} earned"
        return parts.joinToString(" · ")
    }

    /** How much of the day's budget is left, 0 to 1, for the bar. */
    fun leftShare(budget: BudgetView): Float =
        if (!budget.left.isFinite() || !budget.budget.isFinite() || budget.budget <= 0) 0f else (budget.left / budget.budget).coerceIn(0.0, 1.0).toFloat()

    /** "bedtime 23:00" or "bedtime until 08:00" for the card's head. */
    fun bedtimeLabel(budget: BudgetView, bedtimeUntil: Instant?, zone: ZoneId): String = when {
        budget.rate.kind == "bedtime" -> bedtimeUntil?.let { "bedtime until ${BudgetText.clock(it, zone)}" } ?: "bedtime now"
        budget.bedtimeAt != null -> "bedtime ${BudgetText.clock(budget.bedtimeAt, zone)}"
        else -> ""
    }

    /** A door's phase ("waiting", "open", "grace") and its words, or null once it is over (the panel's doorStatus). */
    fun door(door: BudgetDoor, now: Instant, zone: ZoneId): Pair<String, String>? {
        if (door.state == "cancelled" || door.state == "ended") return null
        val ends = door.endsAt
        if (door.state != "waiting" && ends != null && !now.isBefore(ends)) return null
        return when (door.state) {
            "waiting" -> "waiting" to (door.opensAt?.takeIf { it.isAfter(now) }?.let { BudgetText.wait(door, now) } ?: "Opening…")
            "grace" -> {
                val left = ends?.let { maxOf(1L, ceil(Duration.between(now, it).seconds / 60.0).toLong()) } ?: 1L
                "grace" to "$left min to close or save"
            }
            else -> {
                val until = ends?.let { ", until ${BudgetText.clock(it, zone)}" } ?: ""
                val use = door.useMinutes
                "open" to if (use != null) "${door.usedMinutes.toLong()} of ${BudgetText.number(use)} min used$until" else "Open${if (door.borrowing) ", borrowing" else ""}$until"
            }
        }
    }

    /** "Saved at 14:20 · the server can't be reached", with the date when it was another day. */
    fun saved(at: Instant?, now: Instant, zone: ZoneId): String {
        if (at == null) return "Nothing saved yet · the server can't be reached"
        val date = at.atZone(zone).toLocalDate()
        val day = if (date == now.atZone(zone).toLocalDate()) "" else "${date.dayOfMonth} ${date.month.getDisplayName(java.time.format.TextStyle.SHORT, java.util.Locale.UK)} "
        return "Saved ${day}at ${BudgetText.clock(at, zone)} · the server can't be reached"
    }

    private fun instant(text: String?): Instant? =
        text?.takeIf { it.isNotBlank() && it != "null" }?.let { runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() }
}
