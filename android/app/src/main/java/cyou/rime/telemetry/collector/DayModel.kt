package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId

data class DayBlock(
    val title: String,
    val start: Instant,
    val end: Instant,
    /** "upcoming", "in_progress" or "past". */
    val status: String,
    /** The block's task has rules, so [inPlanMinutes] is time on the task itself. */
    val tracked: Boolean,
    /** A focus block with no task: it only closes distractions and has no time of its own. */
    val focusOnly: Boolean,
    val inPlanMinutes: Double,
    val distractionMinutes: Double,
)

/** One of the owner's open tasks with the time recognised as it; untracked tasks have no rules yet. */
data class DayTask(
    val id: Long,
    val title: String,
    val tracked: Boolean,
    /** The owner's own attention on it. */
    val minutes: Double,
    /** Its agent's runtime, and how long the agent sat stopped until the owner came back. */
    val agentMinutes: Double = 0.0,
    val waitingMinutes: Double = 0.0,
    /** Set while its agent has stopped and waits for the owner. */
    val yourTurnSince: Instant? = null,
)

/** Time on one app or site; [detail] narrows it, such as the T3 Code project and thread. */
data class DayItem(val name: String, val detail: String?, val label: String, val minutes: Double, val visits: Int, val device: String = "")

data class DaySegment(val start: Instant, val end: Instant, val name: String, val detail: String?, val title: String?, val label: String, val device: String = "")

/** A stretch of continuous use of one device; items total it, segments are the records behind it. */
data class DayChunk(
    val device: String,
    val start: Instant,
    val end: Instant,
    /** "distraction", "task" or "other". */
    val kind: String,
    /** The task most of a "task" chunk went to. */
    val task: String?,
    val activeMinutes: Double,
    val items: List<DayItem>,
    val segments: List<DaySegment>,
)

data class DayGap(val device: String, val start: Instant, val end: Instant)

/** One day as the server recorded it: the plan, the two totals and the compressed timeline. */
data class DayView(
    val date: String,
    val from: Instant,
    val to: Instant,
    val blocks: List<DayBlock>,
    val tasks: List<DayTask>,
    val plannedMinutes: Double,
    val inPlanMinutes: Double,
    val distractionMinutes: Double,
    val distractionByDevice: List<Pair<String, Double>>,
    val chunks: List<DayChunk>,
    val gaps: List<DayGap>,
)

object DayModel {
    const val DISTRACTION = "distraction"
    const val TASK = "task"

    private fun <T> JSONArray?.each(read: (JSONObject) -> T): List<T> =
        if (this == null) emptyList() else (0 until length()).map { read(getJSONObject(it)) }

    fun parse(json: String): DayView {
        val root = JSONObject(json)
        val day = root.getJSONObject("day")
        return DayView(
            date = root.getString("date"),
            from = Instant.parse(root.getString("from")),
            to = Instant.parse(root.getString("to")),
            blocks = day.optJSONArray("blocks").each {
                DayBlock(it.getString("title"), Instant.parse(it.getString("start")), Instant.parse(it.getString("end")), it.getString("status"),
                    it.optBoolean("tracked"), it.isNull("task_id") && it.optBoolean("closes_distractions"), it.optDouble("in_plan_minutes", 0.0), it.optDouble("distraction_minutes", 0.0))
            },
            tasks = day.optJSONArray("tasks").each {
                DayTask(it.optLong("id"), it.getString("title"), it.optBoolean("tracked"), it.optDouble("minutes", 0.0), it.optDouble("agent_minutes", 0.0),
                    it.optDouble("waiting_minutes", 0.0), it.optString("your_turn_since").ifBlank { null }?.let(Instant::parse))
            },
            plannedMinutes = day.optDouble("planned_minutes", 0.0),
            inPlanMinutes = day.optDouble("in_plan_minutes", 0.0),
            distractionMinutes = day.optDouble("distraction_minutes", 0.0),
            distractionByDevice = day.optJSONArray("distraction_by_device").each { it.getString("device") to it.optDouble("minutes", 0.0) },
            chunks = day.optJSONArray("chunks").each { chunk ->
                DayChunk(
                    device = chunk.getString("device"),
                    start = Instant.parse(chunk.getString("start")),
                    end = Instant.parse(chunk.getString("end")),
                    kind = chunk.optString("kind"),
                    task = chunk.optString("task").ifBlank { null },
                    activeMinutes = chunk.optDouble("active_minutes", 0.0),
                    items = chunk.optJSONArray("items").each {
                        DayItem(it.getString("name"), it.optString("detail").ifBlank { null }, it.optString("label"), it.optDouble("minutes", 0.0), it.optInt("visits"), chunk.getString("device"))
                    },
                    segments = chunk.optJSONArray("segments").each {
                        DaySegment(Instant.parse(it.getString("start")), Instant.parse(it.getString("end")), it.getString("name"),
                            it.optString("detail").ifBlank { null }, it.optString("title").ifBlank { null }, it.optString("label"), chunk.getString("device"))
                    },
                )
            },
            gaps = day.optJSONArray("no_data").each { DayGap(it.getString("device"), Instant.parse(it.getString("start")), Instant.parse(it.getString("end"))) },
        )
    }

    /** "1h 12m", "41m", or "40s" under a minute. */
    fun clock(minutes: Double): String {
        if (minutes > 0 && minutes < 1) return "${(minutes * 60).toInt().coerceAtLeast(1)}s"
        val total = Math.round(minutes).toInt()
        return when {
            total < 60 -> "${total}m"
            total % 60 == 0 -> "${total / 60}h"
            else -> "${total / 60}h ${total % 60}m"
        }
    }

    /** Totals per app or site and device across chunks, biggest first. */
    fun grouped(chunks: List<DayChunk>): List<DayItem> =
        chunks.flatMap { it.items }.groupBy { Triple(it.device, it.name, it.detail) }.map { (_, same) ->
            same.first().copy(minutes = same.sumOf { it.minutes }, visits = same.sumOf { it.visits })
        }.sortedWith(compareByDescending<DayItem> { it.minutes }.thenBy { it.name })

    fun deviceName(device: String): String = when (device) {
        "mac" -> "Mac"
        "phone" -> "Phone"
        else -> device
    }

    /** "Today", "Yesterday" or "Tue 30 Sep" for a YYYY-MM-DD date. */
    fun dayLabel(date: String, now: Instant, zone: ZoneId = ZoneId.systemDefault()): String {
        val day = runCatching { java.time.LocalDate.parse(date) }.getOrNull() ?: return date
        val current = now.atZone(zone).toLocalDate()
        return when (day) {
            current -> "Today"
            current.minusDays(1) -> "Yesterday"
            else -> day.format(java.time.format.DateTimeFormatter.ofPattern("EEE d MMM", java.util.Locale.ENGLISH))
        }
    }

    /** The owner's current day, as the server names days. */
    fun today(now: Instant, zone: ZoneId = ZoneId.systemDefault()): String = now.atZone(zone).toLocalDate().toString()

    /** Whether the answer is for the current day. */
    fun isToday(view: DayView, now: Instant, zone: ZoneId = ZoneId.systemDefault()): Boolean = view.date == today(now, zone)

    /** The hours the timeline spans: from the first activity or block to now or the last block. */
    fun window(view: DayView, zone: ZoneId): Pair<Instant, Instant> {
        val starts = view.chunks.map { it.start } + view.blocks.map { it.start }
        // A finished day ends at its last activity or block, not at midnight.
        val finished = !view.to.isBefore(view.from.plusSeconds(23 * 3600))
        // The timeline runs to the end of everything planned, so no block is cut
        // off at its bottom; a block that runs past the day ends at midnight.
        val dayEnd = view.from.plusSeconds(24 * 3600)
        val ends = view.blocks.map { if (it.end.isAfter(dayEnd)) dayEnd else it.end } + view.chunks.map { it.end } +
            (if (finished && view.chunks.isNotEmpty()) emptyList() else listOf(view.to))
        val first = (starts.minOrNull() ?: view.from).coerceAtLeast(view.from)
        val start = first.atZone(zone).withMinute(0).withSecond(0).withNano(0).toInstant()
        val last = ends.max()
        val floor = last.atZone(zone).withMinute(0).withSecond(0).withNano(0)
        val end = (if (floor.toInstant() == last) floor else floor.plusHours(1)).toInstant()
        return start to end
    }
}

/** Day answers without segments, by date, shared by the home screen and kept for offline. */
object DayCache {
    private const val PREFS = "telemetry-days"
    private const val KEEP = 14

    /** The cached answer for a date (YYYY-MM-DD), if it is for that date. */
    fun read(context: Context, date: String): DayView? {
        val json = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(date, null) ?: return null
        return runCatching { DayModel.parse(json) }.getOrNull()?.takeIf { it.date == date }
    }

    /** Fetches and stores a date; null when offline or unpaired. Blocking IO. */
    fun refresh(context: Context, date: String): DayView? {
        val body = DeviceApi.get(context, "/v1/device/day?date=$date") ?: return null
        val view = runCatching { DayModel.parse(body) }.getOrNull()?.takeIf { it.date == date } ?: return null
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val editor = preferences.edit().putString(date, body)
        // Dates sort as text; only the most recent ones are kept.
        (preferences.all.keys + date).sortedDescending().drop(KEEP).forEach { editor.remove(it) }
        editor.apply()
        return view
    }

    /** A date with every record behind each chunk, for the details page. Not cached. Blocking IO. */
    fun details(context: Context, date: String): DayView? =
        DeviceApi.get(context, "/v1/device/day?date=$date&segments=1")?.let { runCatching { DayModel.parse(it) }.getOrNull() }
}

/** This phone's names for installed apps; other names are shown as recorded. */
class AppNames(private val context: Context) {
    private val known = HashMap<String, String>()

    fun of(raw: String): String = known.getOrPut(raw) {
        runCatching {
            val manager = context.packageManager
            manager.getApplicationLabel(manager.getApplicationInfo(raw, 0)).toString()
        }.getOrDefault(raw.removePrefix("www."))
    }
}
