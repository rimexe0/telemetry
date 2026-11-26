package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import kotlin.math.ceil
import kotlin.math.roundToLong

/** The rate in force: its total and the parts it adds up from. */
data class BudgetRate(val total: Double, val base: Double, val grayscaleOff: Double, val kind: String)

/** A door: asked for, waiting, open, in grace, or over. */
data class BudgetDoor(
    val id: Long,
    /** "waiting", "open", "grace", "ended" or "cancelled". */
    val state: String,
    val requestedAt: Instant?,
    val opensAt: Instant?,
    val windowEnd: Instant?,
    /** The sliding end: when it closes if use goes on from now. */
    val endsAt: Instant?,
    val usedMinutes: Double,
    /** The use one window allows; null inside a distraction time. */
    val useMinutes: Double?,
    val borrowing: Boolean,
) {
    val waiting: Boolean get() = state == "waiting"
    val open: Boolean get() = state == "open" || state == "grace"
}

data class BudgetHabit(val id: Long, val name: String, val done: Boolean, val value: Double)

data class BudgetSettings(
    val waitMinutes: Double,
    val windowMinutes: Double,
    val windowUseMinutes: Double,
    val graceMinutes: Double,
    val borrowMultiplier: Double,
    /** What declining grayscale adds to the rate; null when the server does not say. */
    val rateGrayscaleOff: Double?,
)

/** The `budget` field of /v1/device/now (docs/budget-and-habits.md). */
data class BudgetView(
    val enabled: Boolean,
    val day: String,
    val budget: Double,
    val spent: Double,
    val left: Double,
    val earned: Double,
    val penalty: Double,
    val debt: Double,
    val debtTomorrow: Double,
    val borrowable: Double,
    val rate: BudgetRate,
    val inDistractionTime: Boolean,
    val nextDistractionTime: Instant?,
    val bedtimeAt: Instant?,
    val windDown: Boolean,
    val grayscaleDeclined: Boolean,
    val door: BudgetDoor?,
    val doorAvailable: Boolean,
    val doorUnavailableReason: String,
    val nextDoorAt: Instant?,
    val habits: List<BudgetHabit>,
    val reasons: List<String>,
    val settings: BudgetSettings,
) {
    /** Today's budget is gone, so more use is charged to tomorrow. */
    val borrowingNow: Boolean get() = left <= 0.0

    /** What declining grayscale adds: the setting, else the surcharge already in force. */
    val grayscaleSurcharge: Double? get() = settings.rateGrayscaleOff ?: rate.grayscaleOff.takeIf { it > 0 }
}

object Budget {
    /** The budget in a /v1/device/now body, or null when the server sends none. */
    fun parse(json: String): BudgetView? = runCatching { JSONObject(json).optJSONObject("budget")?.let(::fromObject) }.getOrNull()

    fun fromObject(budget: JSONObject): BudgetView {
        val rate = budget.optJSONObject("rate") ?: JSONObject()
        val settings = budget.optJSONObject("settings") ?: JSONObject()
        return BudgetView(
            enabled = budget.optBoolean("enabled"),
            day = budget.optString("day"),
            budget = budget.optDouble("budget", 0.0),
            spent = budget.optDouble("spent", 0.0),
            left = budget.optDouble("left", 0.0),
            earned = budget.optDouble("earned", 0.0),
            penalty = budget.optDouble("penalty", 0.0),
            debt = budget.optDouble("debt", 0.0),
            debtTomorrow = budget.optDouble("debt_tomorrow", 0.0),
            borrowable = budget.optDouble("borrowable", 0.0),
            rate = BudgetRate(
                total = rate.optDouble("total", 1.0),
                base = rate.optDouble("base", rate.optDouble("total", 1.0)),
                grayscaleOff = rate.optDouble("grayscale_off", 0.0),
                kind = rate.optString("kind"),
            ),
            inDistractionTime = budget.optBoolean("in_distraction_time"),
            nextDistractionTime = instant(budget, "next_distraction_time"),
            bedtimeAt = instant(budget, "bedtime_at"),
            windDown = budget.optBoolean("wind_down"),
            grayscaleDeclined = budget.optBoolean("grayscale_declined"),
            door = budget.optJSONObject("door")?.let(::door),
            doorAvailable = budget.optBoolean("door_available"),
            doorUnavailableReason = budget.optString("door_unavailable_reason"),
            nextDoorAt = instant(budget, "next_door_at"),
            habits = objects(budget.optJSONArray("habits")).map {
                BudgetHabit(it.optLong("id"), it.optString("name"), it.optBoolean("done"), it.optDouble("value", 0.0))
            },
            reasons = strings(budget.optJSONArray("reasons")),
            settings = BudgetSettings(
                waitMinutes = settings.optDouble("wait_minutes", 0.0),
                windowMinutes = settings.optDouble("window_minutes", 0.0),
                windowUseMinutes = settings.optDouble("window_use_minutes", 0.0),
                graceMinutes = settings.optDouble("grace_minutes", 0.0),
                borrowMultiplier = settings.optDouble("borrow_multiplier", 1.0),
                rateGrayscaleOff = if (settings.has("rate_grayscale_off") && !settings.isNull("rate_grayscale_off")) settings.optDouble("rate_grayscale_off") else null,
            ),
        )
    }

    fun door(door: JSONObject): BudgetDoor = BudgetDoor(
        id = door.optLong("id"),
        state = door.optString("state"),
        requestedAt = instant(door, "requested_at"),
        opensAt = instant(door, "opens_at"),
        windowEnd = instant(door, "window_end"),
        endsAt = instant(door, "ends_at"),
        usedMinutes = door.optDouble("used_minutes", 0.0),
        useMinutes = if (door.has("use_minutes") && !door.isNull("use_minutes")) door.optDouble("use_minutes") else null,
        borrowing = door.optBoolean("borrowing"),
    )

    /** The message of a refusal (`{error, message}`), as the server gave it. */
    fun refusal(body: String): String? = runCatching {
        val json = JSONObject(body)
        json.optString("message").ifBlank { json.optString("error") }.ifBlank { null }
    }.getOrNull()

    private fun instant(json: JSONObject, key: String): Instant? {
        if (!json.has(key) || json.isNull(key)) return null
        val text = json.optString(key).ifBlank { return null }
        return runCatching { OffsetDateTime.parse(text).toInstant() }.getOrNull()
    }

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }

    private fun strings(array: JSONArray?): List<String> =
        if (array == null) emptyList() else (0 until array.length()).map { array.optString(it) }.filter { it.isNotBlank() }
}

/** The words the door screen, the counter and the home screen use. They state facts and never scold. */
object BudgetText {
    /** "45 min", "1 h", "1 h 40 min". */
    fun minutes(value: Double): String {
        val total = value.coerceAtLeast(0.0).roundToLong()
        return when {
            total < 60 -> "$total min"
            total % 60 == 0L -> "${total / 60} h"
            else -> "${total / 60} h ${total % 60} min"
        }
    }

    /** 3 → "3", 2.5 → "2.5". */
    fun number(value: Double): String =
        if (value == Math.floor(value) && !value.isInfinite()) value.toLong().toString() else String.format(java.util.Locale.ROOT, "%.1f", value)

    private fun kindLabel(kind: String): String = when (kind) {
        "distraction" -> "distraction time"
        "day" -> "daytime"
        "bedtime" -> "bedtime"
        else -> kind
    }

    /** "3× now." or, with its parts, "13× now: 10× bedtime + 3× grayscale off." */
    fun rate(rate: BudgetRate): String {
        val total = "${number(rate.total)}× now"
        if (rate.grayscaleOff <= 0) return "$total."
        return "$total: ${number(rate.base)}× ${kindLabel(rate.kind)} + ${number(rate.grayscaleOff)}× grayscale off."
    }

    /** What a window of use costs now, and what is left or how borrowing stands. */
    fun cost(budget: BudgetView): String {
        val use = budget.settings.windowUseMinutes.takeIf { it > 0 } ?: 10.0
        return if (budget.borrowingNow) {
            val cost = use * budget.rate.total * budget.settings.borrowMultiplier
            "Borrowing: ${minutes(use)} = ${minutes(cost)} of tomorrow's budget. Tomorrow can still lend ${minutes(budget.borrowable)}."
        } else {
            "${minutes(use)} = ${minutes(use * budget.rate.total)} of budget. Left today: ${minutes(budget.left)}."
        }
    }

    /** One line per habit: done ones, and open ones with what each would earn. */
    fun habit(habit: BudgetHabit): String =
        if (habit.done) "${habit.name} · done" else "${habit.name} · +${minutes(habit.value)}"

    /** "Bedtime in 25 min" during the wind-down; "It's bedtime (until 08:00)." during bedtime; else null. */
    fun bedtime(budget: BudgetView, now: Instant, bedtimeUntil: Instant?, zone: ZoneId): String? {
        if (budget.rate.kind == "bedtime") {
            return if (bedtimeUntil != null) "It's bedtime (until ${clock(bedtimeUntil, zone)})." else "It's bedtime."
        }
        val at = budget.bedtimeAt ?: return null
        if (!budget.windDown || !at.isAfter(now)) return null
        return "Bedtime in ${minutes(ceil(Duration.between(now, at).seconds / 60.0))}"
    }

    /** Why no door can open now, with when the next one can. */
    fun unavailable(budget: BudgetView, zone: ZoneId): String {
        val reason = budget.doorUnavailableReason.ifBlank { "No door can open right now." }
        val next = budget.nextDoorAt ?: return reason
        return "$reason The next door can open at ${clock(next, zone)}."
    }

    /** "Opens in 7:32" while a door waits. */
    fun wait(door: BudgetDoor, now: Instant): String {
        val opens = door.opensAt ?: return "Opening soon"
        val seconds = Duration.between(now, opens).seconds.coerceAtLeast(0)
        return String.format(java.util.Locale.ROOT, "Opens in %d:%02d", seconds / 60, seconds % 60)
    }

    fun clock(at: Instant, zone: ZoneId): String {
        val local = at.atZone(zone)
        return String.format(java.util.Locale.ROOT, "%02d:%02d", local.hour, local.minute)
    }
}
