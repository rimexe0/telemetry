package cyou.rime.telemetry.collector

import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.OffsetDateTime
import java.time.ZoneId
import java.util.Locale
import kotlin.math.roundToLong

/**
 * A habit rule (owner request 2026-11-26): the habit ticks itself once the
 * phone's positions show the owner within [radiusM] of a place they picked
 * for [minutes], inside a window of the budget day. The server's
 * `budget.HabitRule` is the same; it checks the rule again from the stored
 * positions as a safety net.
 */
data class HabitRule(
    val name: String,
    val lat: Double,
    val lon: Double,
    val radiusM: Int = 100,
    /** "wake" or a local "HH:MM". */
    val from: String,
    /** A local "HH:MM". */
    val until: String,
    val minutes: Int = 1,
) {
    val fromWake: Boolean get() = from == WAKE

    fun toJson(): JSONObject = JSONObject()
        .put("kind", "place")
        .put("place", JSONObject().put("name", name).put("lat", lat).put("lon", lon))
        .put("radius_m", radiusM)
        .put("window", JSONObject().put("from", from).put("until", until))
        .put("minutes", minutes)

    companion object {
        const val WAKE = "wake"

        fun parse(json: JSONObject?): HabitRule? {
            json ?: return null
            if (json.optString("kind") != "place") return null
            val place = json.optJSONObject("place") ?: return null
            val window = json.optJSONObject("window") ?: return null
            val lat = place.optDouble("lat")
            val lon = place.optDouble("lon")
            val until = window.optString("until")
            val from = window.optString("from")
            if (lat.isNaN() || lon.isNaN() || HabitRuleEval.clock(until) == null || (from != WAKE && HabitRuleEval.clock(from) == null)) return null
            return HabitRule(
                name = place.optString("name").ifBlank { "the place" },
                lat = lat,
                lon = lon,
                radiusM = json.optInt("radius_m", 100).takeIf { it > 0 } ?: 100,
                from = from,
                until = until,
                minutes = json.optInt("minutes", 1).takeIf { it > 0 } ?: 1,
            )
        }

        /** The rule field of a habit's JSON: null, missing or unreadable is none. */
        fun of(habit: JSONObject): HabitRule? = if (habit.isNull("rule")) null else parse(habit.optJSONObject("rule"))
    }
}

/** One of the phone's positions. */
data class RuleFix(val at: Instant, val lat: Double, val lon: Double, val accuracy: Double)

/** A rule's window on one budget day. */
data class RuleWindow(val from: Instant, val until: Instant, val wakeKnown: Boolean) {
    fun open(now: Instant): Boolean = !now.isBefore(from) && now.isBefore(until)

    companion object {
        /** The server's resolved window, from a habit of the budget. */
        fun parse(json: JSONObject?): RuleWindow? {
            json ?: return null
            val from = runCatching { OffsetDateTime.parse(json.optString("from")).toInstant() }.getOrNull() ?: return null
            val until = runCatching { OffsetDateTime.parse(json.optString("until")).toInstant() }.getOrNull() ?: return null
            return RuleWindow(from, until, json.optBoolean("wake_known"))
        }
    }
}

/**
 * The rule worked out on the phone, as the server works it out
 * (internal/budget/rules.go), so a tick is the same whichever makes it.
 */
object HabitRuleEval {
    /** The poorest position that counts, as for the outside rule. */
    const val WORST_ACCURACY = 100.0
    /** The longest gap between two positions inside that still counts as staying. */
    val GAP: Duration = Duration.ofMinutes(15)

    fun clock(text: String): LocalTime? =
        if (Regex("\\d{2}:\\d{2}").matches(text)) runCatching { LocalTime.parse(text) }.getOrNull() else null

    /**
     * The rule's window on [day]. The server's, when it gave one for [day]
     * (it knows the wake). Otherwise worked out here: a window from waking
     * opens at the day's start, the end of bedtime, since the phone does not
     * know the wake; it never runs past [closeAt], when the day's habits
     * close. Null when nothing is left of it.
     */
    fun window(rule: HabitRule, day: String, zone: ZoneId, dayStart: LocalTime, closeAt: Instant?, server: RuleWindow?): RuleWindow? {
        if (server != null) return server.takeIf { it.from.isBefore(it.until) }
        val date = runCatching { LocalDate.parse(day) }.getOrNull() ?: return null
        val start = date.atTime(dayStart).atZone(zone).toInstant()
        fun on(text: String): Instant? {
            val time = clock(text) ?: return null
            val at = date.atTime(time).atZone(zone).toInstant()
            return if (at.isBefore(start)) date.plusDays(1).atTime(time).atZone(zone).toInstant() else at
        }
        var until = on(rule.until) ?: return null
        if (closeAt != null && until.isAfter(closeAt)) until = closeAt
        val from = if (rule.fromWake) start else on(rule.from) ?: return null
        return RuleWindow(from, until, wakeKnown = false).takeIf { from.isBefore(until) }
    }

    fun inside(rule: HabitRule, fix: RuleFix): Boolean =
        fix.accuracy <= WORST_ACCURACY && PlaceRule.meters(fix.lat, fix.lon, rule.lat, rule.lon) <= rule.radiusM

    /**
     * When the positions first add up to the rule's minutes inside the radius
     * within [from, until): stretches between consecutive positions both
     * inside and at most [GAP] apart. Null when they do not: missing
     * positions are never filled in.
     */
    fun reached(rule: HabitRule, fixes: List<RuleFix>, from: Instant, until: Instant): Instant? {
        if (!from.isBefore(until)) return null
        val ordered = fixes.sortedBy { it.at }
        val need = Duration.ofMinutes(rule.minutes.toLong())
        var total = Duration.ZERO
        var end: Instant? = null
        for (i in 1 until ordered.size) {
            val a = ordered[i - 1]
            val b = ordered[i]
            val gap = Duration.between(a.at, b.at)
            if (gap <= Duration.ZERO || gap > GAP || !inside(rule, a) || !inside(rule, b)) continue
            var start = maxOf(a.at, from)
            val stop = minOf(b.at, until)
            // One device's stretches do not overlap, but a clipped one may touch the last.
            end?.let { if (start.isBefore(it)) start = it }
            if (!stop.isAfter(start)) continue
            val length = Duration.between(start, stop)
            if (total + length >= need) {
                val exact = start.plus(need - total)
                val rounded = if (exact.nano == 0) exact else Instant.ofEpochSecond(exact.epochSecond + 1)
                return minOf(rounded, stop)
            }
            total += length
            end = stop
        }
        return null
    }

    /**
     * Whether the rule may tick a habit today: not done, not unticked by the
     * owner, nothing of the owner's waiting in the queue for it, and not
     * already ticked by the rule here. The owner's word wins.
     */
    fun mayTick(done: Boolean, unticked: Boolean, queuedToday: Boolean, firedToday: Boolean): Boolean =
        !done && !unticked && !queuedToday && !firedToday
}

/** The words for a habit's rule on Today, Habits, the widget and the morning notice. */
object RuleText {
    /** A position older than this is not shown as where the phone is. */
    val LATEST_FOR: Duration = Duration.ofHours(12)

    /** "40 m", "420 m", "1.2 km", "12 km". */
    fun distance(meters: Double): String = when {
        meters < 1000 -> "${(meters / 10).roundToLong() * 10} m"
        meters < 10_000 -> String.format(Locale.ROOT, "%.1f km", meters / 1000)
        else -> "${(meters / 1000).roundToLong()} km"
    }

    /** The latest position, while it is recent enough to say where the phone is. */
    fun usable(latest: RuleFix?, now: Instant): RuleFix? =
        latest?.takeIf { it.accuracy <= HabitRuleEval.WORST_ACCURACY && Duration.between(it.at, now) <= LATEST_FOR && !it.at.isAfter(now.plusSeconds(120)) }

    /**
     * Under a habit with a rule: "Karantina İskelesi · 420 m away", or
     * "· location unknown" without a recent position; "reached 08:12" once
     * the rule ticked it. Null when the owner ticked it by hand.
     */
    fun line(rule: HabitRule, done: Boolean, doneBy: String?, doneAt: Instant?, latest: RuleFix?, now: Instant, zone: ZoneId): String? {
        if (done) return if (doneBy == "rule" && doneAt != null) "reached ${BudgetText.clock(doneAt, zone)}" else null
        val fix = usable(latest, now) ?: return "${rule.name} · location unknown"
        return "${rule.name} · ${distance(PlaceRule.meters(fix.lat, fix.lon, rule.lat, rule.lon))} away"
    }

    /** "Going outside after waking up: Karantina İskelesi is 420 m away." */
    fun notice(habitName: String, rule: HabitRule, latest: RuleFix?, now: Instant): String {
        val fix = usable(latest, now) ?: return "$habitName: go to ${rule.name}."
        return "$habitName: ${rule.name} is ${distance(PlaceRule.meters(fix.lat, fix.lon, rule.lat, rule.lon))} away."
    }

    /** The Habits screen's rule row: "Karantina İskelesi · 100 m · from waking until 12:00 · 1 min". */
    fun summary(rule: HabitRule?): String {
        rule ?: return "No rule"
        val from = if (rule.fromWake) "from waking" else "from ${rule.from}"
        return "${rule.name} · ${rule.radiusM} m · $from until ${rule.until} · ${rule.minutes} min"
    }
}

/** The morning notice: once a day per habit, at the window's start while the owner uses the phone. */
object RuleNotice {
    /**
     * Due when the window is open, the habit isn't done or unticked, the
     * screen is on (the owner is up and can read it), and it was not said
     * today already. [said] is the budget day it was last said, or null.
     */
    fun due(window: RuleWindow?, now: Instant, done: Boolean, unticked: Boolean, interactive: Boolean, day: String, said: String?): Boolean =
        window != null && window.open(now) && !done && !unticked && interactive && said != day
}

/** The picker's fields as typed, until they make a rule. */
data class RuleDraft(
    val name: String = "",
    val lat: Double? = null,
    val lon: Double? = null,
    val radius: String = "100",
    val fromWake: Boolean = true,
    val from: String = "",
    val until: String = "",
    val minutes: String = "1",
) {
    /** The rule, or what is missing in the owner's words. */
    fun rule(): Pair<HabitRule?, String?> {
        val name = name.trim()
        val radius = radius.trim().toIntOrNull()
        val minutes = minutes.trim().toIntOrNull()
        val from = from.trim()
        val until = until.trim()
        return when {
            lat == null || lon == null -> null to "Pick the place on the map or by name."
            name.isEmpty() || name.length > 100 -> null to "Give the place a name."
            radius == null || radius !in 20..2000 -> null to "The distance is 20 to 2000 m."
            !fromWake && HabitRuleEval.clock(from) == null -> null to "From is a time such as 07:30."
            HabitRuleEval.clock(until) == null -> null to "Until is a time such as 11:00."
            !fromWake && from == until -> null to "From and until are the same time."
            minutes == null || minutes !in 1..240 -> null to "The minutes there are 1 to 240."
            else -> HabitRule(name, lat, lon, radius, if (fromWake) HabitRule.WAKE else from, until, minutes) to null
        }
    }

    companion object {
        fun of(rule: HabitRule?): RuleDraft = if (rule == null) RuleDraft() else RuleDraft(
            name = rule.name, lat = rule.lat, lon = rule.lon, radius = rule.radiusM.toString(), fromWake = rule.fromWake,
            from = if (rule.fromWake) "" else rule.from, until = rule.until, minutes = rule.minutes.toString(),
        )
    }
}

/** The lines under today's habits that have a rule. */
fun ruleLines(habits: List<TodayHabit>?, latest: RuleFix?, now: Instant, zone: ZoneId): Map<Long, String> =
    habits.orEmpty().mapNotNull { habit ->
        val rule = habit.rule ?: return@mapNotNull null
        RuleText.line(rule, habit.done, habit.doneBy, habit.doneAt, latest, now, zone)?.let { habit.id to it }
    }.toMap()
