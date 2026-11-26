package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.time.DayOfWeek
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.OffsetDateTime
import java.time.ZoneId

/** One of the owner's habits as /v1/device/habits gives it. */
data class HabitDef(
    val id: Long,
    val name: String,
    val position: Int,
    /** 0 Sunday to 6 Saturday; empty means every day. */
    val weekdays: Set<Int>,
    val archived: Boolean,
)

/** /v1/device/habits: every habit, and the owner's reasons. */
data class HabitList(val habits: List<HabitDef>, val reasons: List<String>) {
    /** Active habits in their order. */
    val active: List<HabitDef> get() = habits.filter { !it.archived }.sortedWith(compareBy({ it.position }, { it.id }))
}

/** One habit's row of the heatmap (the panel's /v1/habits/heatmap). */
data class HeatmapRow(
    val id: Long,
    val name: String,
    val createdDay: String,
    val archivedDay: String?,
    /** Day key to "done", "missed", "unscheduled" or "none". */
    val days: Map<String, String>,
    val currentRun: Int,
    val longestRun: Int,
)

data class HeatmapView(val habits: List<HeatmapRow>, val combined: Map<String, Double>)

/** One of today's habits as Today shows it. */
data class TodayHabit(val id: Long, val name: String, val done: Boolean, /** Budget minutes it is worth; null when unknown. */ val value: Double?)

object HabitJson {
    fun list(json: String): HabitList? = runCatching {
        val root = JSONObject(json)
        val habits = objects(root.optJSONArray("habits")).map { item ->
            val weekdays = item.optJSONArray("weekdays")
            HabitDef(
                id = item.getLong("id"),
                name = item.optString("name"),
                position = item.optInt("position"),
                weekdays = if (weekdays == null) emptySet() else (0 until weekdays.length()).map { weekdays.optInt(it, -1) % 7 }.filter { it in 0..6 }.toSet(),
                archived = item.optBoolean("archived") || (item.has("archived_at") && !item.isNull("archived_at") && item.optString("archived_at").isNotBlank()),
            )
        }
        HabitList(habits, strings(root.optJSONArray("reasons")))
    }.getOrNull()

    fun heatmap(json: String): HeatmapView? = runCatching {
        val root = JSONObject(json)
        val rows = objects(root.optJSONArray("habits")).map { item ->
            val days = item.optJSONObject("days")
            HeatmapRow(
                id = item.getLong("id"),
                name = item.optString("name"),
                createdDay = item.optString("created_day"),
                archivedDay = if (item.isNull("archived_day")) null else item.optString("archived_day").ifBlank { null },
                days = days?.keys()?.asSequence()?.associateWith { days.optString(it) }.orEmpty(),
                currentRun = item.optInt("current_run"),
                longestRun = item.optInt("longest_run"),
            )
        }
        val combined = root.optJSONObject("combined")
        HeatmapView(rows, combined?.keys()?.asSequence()?.mapNotNull { key -> combined.optDouble(key).takeIf { it.isFinite() }?.let { key to it } }?.toMap().orEmpty())
    }.getOrNull()

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }

    private fun strings(array: JSONArray?): List<String> =
        if (array == null) emptyList() else (0 until array.length()).map { array.optString(it) }.filter { it.isNotBlank() }
}

/**
 * The habit answers saved on the phone, so Today and Habits show them at once
 * and keep working while the server is down. Each is the server's body as it
 * came, with when it came.
 */
object HabitCache {
    private const val PREFS = "telemetry-habits"
    const val LIST = "habits"
    const val WEEK = "heatmap-7"
    const val YEAR = "heatmap-371"

    private val paths = mapOf(
        LIST to "/v1/device/habits",
        WEEK to "/v1/device/habits/heatmap?days=7",
        YEAR to "/v1/device/habits/heatmap?days=371",
    )

    fun read(context: Context, key: String): NowCache.Entry? {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val json = preferences.getString(key, null) ?: return null
        val at = preferences.getString("$key:at", null)?.let { runCatching { Instant.parse(it) }.getOrNull() } ?: return null
        return NowCache.Entry(json, at)
    }

    /** Fetches and keeps one answer; false when the server did not give it. Blocking IO. */
    fun refresh(context: Context, key: String): Boolean {
        val body = DeviceApi.get(context, paths.getValue(key)) ?: return false
        // Only an answer that reads is kept, so a broken one never replaces a good copy.
        val readable = if (key == LIST) HabitJson.list(body) != null else HabitJson.heatmap(body) != null
        if (!readable) return false
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(key, body)
            .putString("$key:at", Instant.now().toString())
            .commit()
        return true
    }

    /**
     * Puts an edit the server accepted into the saved list, from the server's
     * answer, so the list agrees with the server before it is fetched again.
     */
    fun patch(context: Context, edit: HabitEdit, answer: JSONObject?) {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val patched = patchList(preferences.getString(LIST, null), edit, answer) ?: return
        preferences.edit().putString(LIST, patched).putString("$LIST:at", Instant.now().toString()).commit()
    }

    /** [patch] on the body itself; null when there is nothing to change. */
    fun patchList(json: String?, edit: HabitEdit, answer: JSONObject?): String? {
        val root = json?.let { runCatching { JSONObject(it) }.getOrNull() } ?: JSONObject().put("habits", JSONArray()).put("reasons", JSONArray())
        if (edit.kind == HabitEdit.REASONS) {
            root.put("reasons", answer?.optJSONArray("reasons") ?: JSONArray(edit.reasons.orEmpty()))
            return root.toString()
        }
        val habit = answer?.optJSONObject("habit") ?: return null
        val id = habit.optLong("id", 0L).takeIf { it > 0 } ?: return null
        val habits = root.optJSONArray("habits") ?: JSONArray()
        val kept = JSONArray()
        var placed = false
        val archived = habit.has("archived_at") && !habit.isNull("archived_at") && habit.optString("archived_at").isNotBlank()
        for (index in 0 until habits.length()) {
            val item = habits.optJSONObject(index) ?: continue
            if (item.optLong("id") != id) kept.put(item)
            else if (!archived) { kept.put(habit); placed = true }
        }
        // The saved list has active habits only.
        if (!archived && !placed) kept.put(habit)
        root.put("habits", kept)
        return root.toString()
    }

    fun list(context: Context): HabitList? = read(context, LIST)?.json?.let(HabitJson::list)

    fun heatmap(context: Context, key: String): HeatmapView? = read(context, key)?.json?.let(HabitJson::heatmap)
}

/**
 * Which budget day it is and until when habits can be ticked, from what the
 * phone has saved. The server's rule (internal/budget/days.go): a day starts
 * at the end of bedtime, or 04:00 with bedtime off, and its key is the local
 * date it starts on. Habits can be ticked until bedtime starts.
 */
object HabitDay {
    private val NO_BEDTIME_START: LocalTime = LocalTime.of(4, 0)

    /** A run of bedtime in the cached timeline. */
    data class Bedtime(val from: Instant, val to: Instant)

    /** The runs of "bedtime" segments in a /v1/device/now body, in order. */
    fun bedtimes(json: String?): List<Bedtime> {
        val timeline = json?.let { runCatching { JSONObject(it).optJSONArray("timeline") }.getOrNull() } ?: return emptyList()
        val runs = mutableListOf<Bedtime>()
        for (index in 0 until timeline.length()) {
            val segment = timeline.optJSONObject(index) ?: continue
            if (segment.optJSONObject("decision")?.optString("mode") != "bedtime") continue
            val from = instant(segment.optString("from")) ?: continue
            val to = instant(segment.optString("to")) ?: continue
            val last = runs.lastOrNull()
            if (last != null && !from.isAfter(last.to)) runs[runs.size - 1] = last.copy(to = maxOf(last.to, to)) else runs += Bedtime(from, to)
        }
        return runs
    }

    /** The end of the timeline the body covers, or null. */
    fun timelineEnd(json: String?): Instant? {
        val timeline = json?.let { runCatching { JSONObject(it).optJSONArray("timeline") }.getOrNull() } ?: return null
        if (timeline.length() == 0) return null
        return instant(timeline.optJSONObject(timeline.length() - 1)?.optString("to"))
    }

    /**
     * When a budget day starts, as a local clock time: the end of a bedtime
     * that the timeline shows whole (the last one, since a door can cut an
     * earlier one short), else 04:00.
     */
    fun dayStartClock(bedtimes: List<Bedtime>, timelineEnd: Instant?, zone: ZoneId): LocalTime {
        val whole = bedtimes.lastOrNull { timelineEnd == null || it.to.isBefore(timelineEnd) } ?: return NO_BEDTIME_START
        return whole.to.atZone(zone).toLocalTime().withSecond(0).withNano(0)
    }

    /** The budget day [now] belongs to. Never earlier than the day the server last named. */
    fun today(now: Instant, zone: ZoneId, startClock: LocalTime, serverDay: String?): String {
        val local = now.atZone(zone)
        val date = if (local.toLocalTime().isBefore(startClock)) local.toLocalDate().minusDays(1) else local.toLocalDate()
        val server = serverDay?.let { runCatching { LocalDate.parse(it) }.getOrNull() }
        return (if (server != null && server.isAfter(date)) server else date).toString()
    }

    /**
     * When ticking closes on [day]: the server's bedtime for the day it last
     * described, else the first bedtime in the timeline that starts after the
     * day does. Null when unknown, and then ticks are allowed.
     */
    fun ticksCloseAt(day: String, zone: ZoneId, startClock: LocalTime, budget: BudgetView?, bedtimes: List<Bedtime>): Instant? {
        if (budget != null && budget.day == day) return budget.bedtimeAt
        val start = runCatching { LocalDate.parse(day).atTime(startClock).atZone(zone).toInstant() }.getOrNull() ?: return null
        return bedtimes.firstOrNull { it.from.isAfter(start) }?.from
    }

    /** Sunday 0 to Saturday 6, as the server counts. */
    fun weekday(day: String): Int? = runCatching { LocalDate.parse(day).dayOfWeek }.getOrNull()?.let { if (it == DayOfWeek.SUNDAY) 0 else it.value }

    private fun instant(text: String?): Instant? =
        text?.takeIf { it.isNotBlank() }?.let { runCatching { OffsetDateTime.parse(it).toInstant() }.getOrNull() }
}

/** Today's habits: the server's own list for the day it last described, else worked out from the habits' weekdays. */
object TodayHabits {
    /**
     * [budget] is the cached budget. When it is for [day] its habits and
     * their ticks are used; otherwise (the server has been down since an
     * earlier day) the active habits scheduled on [day]'s weekday, none
     * ticked and their worth unknown. Queued ticks for [day] go on top.
     * Null when nothing saved says what today's habits are.
     */
    fun list(day: String, budget: BudgetView?, habits: HabitList?, queued: List<HabitTick>): List<TodayHabit>? {
        val base = if (budget != null && budget.day == day) {
            budget.habits.map { TodayHabit(it.id, it.name, it.done, it.value) }
        } else {
            val weekday = HabitDay.weekday(day) ?: return null
            habits?.active?.filter { it.weekdays.isEmpty() || weekday in it.weekdays }?.map { TodayHabit(it.id, it.name, false, null) } ?: return null
        }
        val overlay = TickQueue.doneByQueue(queued, day)
        return base.map { habit -> overlay[habit.id]?.let { habit.copy(done = it) } ?: habit }
    }
}

/** How one day of a habit is drawn, as on the panel. */
enum class CellState { DONE, MISSED, UNSCHEDULED, NONE, OPEN, UNKNOWN }

data class WeekCell(val day: String, val state: CellState, val letter: Char, val today: Boolean)

object HabitCells {
    private val letters = charArrayOf('S', 'M', 'T', 'W', 'T', 'F', 'S')

    fun recorded(status: String?): CellState? = when (status) {
        "done" -> CellState.DONE
        "missed" -> CellState.MISSED
        "unscheduled" -> CellState.UNSCHEDULED
        "none" -> CellState.NONE
        else -> null
    }

    /**
     * The last [count] days of one habit, oldest first, ending on [today].
     * Today is done once ticked and open until then, unless the habit is not
     * scheduled or did not exist yet. Earlier days are what the server
     * recorded, with a tick still waiting to be sent counted as done; a day
     * without a record stays unknown, never missed (the panel's weekStrip).
     */
    fun week(days: Map<String, String>?, today: String, todayDone: Boolean, queuedDone: Map<String, Boolean> = emptyMap(), count: Int = 7): List<WeekCell> {
        val end = LocalDate.parse(today)
        return (count - 1 downTo 0).map { back ->
            val date = end.minusDays(back.toLong())
            val key = date.toString()
            val recorded = recorded(days?.get(key))
            val state = if (back == 0) {
                when {
                    todayDone -> CellState.DONE
                    recorded == CellState.UNSCHEDULED || recorded == CellState.NONE -> recorded
                    else -> CellState.OPEN
                }
            } else when (queuedDone[key]) {
                true -> CellState.DONE
                else -> recorded ?: CellState.UNKNOWN
            }
            WeekCell(key, state, letters[date.dayOfWeek.value % 7], back == 0)
        }
    }

    /** A heatmap cell: today stays open until the day closes, unless done, unscheduled or not yet there. */
    fun year(status: String?, day: String, openDay: String): CellState {
        val recorded = recorded(status)
        if (day == openDay && recorded != CellState.DONE && recorded != CellState.UNSCHEDULED && recorded != CellState.NONE) return CellState.OPEN
        return recorded ?: CellState.UNKNOWN
    }

    /** The shade of a combined cell, 0 to 4, or -1 without a record. */
    fun level(share: Double?): Int = when {
        share == null || !share.isFinite() -> -1
        share <= 0 -> 0
        share >= 1 -> 4
        else -> minOf(3, kotlin.math.ceil(share * 3).toInt())
    }

    private fun days(count: Int) = "$count ${if (count == 1) "day" else "days"}"

    /** The runs as facts; nothing ever says a run ended (the panel's runsText). */
    fun runs(current: Int, longest: Int): String {
        val best = maxOf(current, longest)
        return when {
            best <= 0 -> "No runs yet"
            current > 0 && current >= best -> "${days(current)} in a row, the longest so far"
            current > 0 -> "${days(current)} in a row · longest ${days(best)}"
            else -> "Longest run ${days(best)}"
        }
    }

    /** The year grid: weeks as columns, Monday first, null where the range does not reach; month names over columns. */
    data class Layout(val weeks: List<List<String?>>, val months: List<Pair<String, Int>>)

    private val monthNames = listOf("Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec")

    /** The GitHub-style grid for [count] days ending on [end] (the panel's heatmapLayout). */
    fun layout(end: String, count: Int): Layout {
        val last = LocalDate.parse(end)
        val start = last.minusDays((maxOf(1, count) - 1).toLong())
        val weeks = mutableListOf<List<String?>>()
        var week = MutableList<String?>(start.dayOfWeek.value - 1) { null }
        var day = start
        while (!day.isAfter(last)) {
            week += day.toString()
            if (week.size == 7) { weeks += week; week = mutableListOf() }
            day = day.plusDays(1)
        }
        if (week.isNotEmpty()) weeks += week + List(7 - week.size) { null }
        val months = mutableListOf<Pair<String, Int>>()
        var month = ""
        weeks.forEachIndexed { index, column ->
            val first = column.firstOrNull { it != null } ?: return@forEachIndexed
            if (first.substring(0, 7) != month) {
                // A label needs about three columns; a part month at the start gives way.
                if (months.isNotEmpty() && index - months.last().second < 3) months.removeAt(months.size - 1)
                months += monthNames[first.substring(5, 7).toInt() - 1] to index
                month = first.substring(0, 7)
            }
        }
        return Layout(weeks, months)
    }

    /** "+30": what a habit is worth today in whole budget minutes, or "" (the panel's habitWorth). */
    fun worth(value: Double?): String {
        if (value == null || !value.isFinite() || value <= 0) return ""
        val rounded = Math.round(value)
        return if (rounded >= 1) "+$rounded" else "+<1"
    }
}
