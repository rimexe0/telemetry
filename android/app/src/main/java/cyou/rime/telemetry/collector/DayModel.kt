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
    val inPlanMinutes: Double,
    val distractionMinutes: Double,
)

/** Time on one app or site; [detail] narrows it, such as the T3 Code project and thread. */
data class DayItem(val name: String, val detail: String?, val label: String, val minutes: Double, val visits: Int, val device: String = "")

data class DaySegment(val start: Instant, val end: Instant, val name: String, val detail: String?, val title: String?, val label: String, val device: String = "")

/** A stretch of continuous use of one device; items total it, segments are the records behind it. */
data class DayChunk(
    val device: String,
    val start: Instant,
    val end: Instant,
    val distraction: Boolean,
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
    val plannedMinutes: Double,
    val inPlanMinutes: Double,
    val distractionMinutes: Double,
    val distractionByDevice: List<Pair<String, Double>>,
    val chunks: List<DayChunk>,
    val gaps: List<DayGap>,
)

object DayModel {
    const val DISTRACTION = "distraction"

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
                    it.optDouble("in_plan_minutes", 0.0), it.optDouble("distraction_minutes", 0.0))
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
                    distraction = chunk.optString("kind") == DISTRACTION,
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

    /** Whether the answer is for the current day; the server names the day. */
    fun isToday(view: DayView, now: Instant, zone: ZoneId = ZoneId.systemDefault()): Boolean =
        view.date == now.atZone(zone).toLocalDate().toString()

    /** The hours the timeline spans: from the first activity or block to now or the last block. */
    fun window(view: DayView, zone: ZoneId): Pair<Instant, Instant> {
        val starts = view.chunks.map { it.start } + view.blocks.map { it.start }
        val ends = view.blocks.map { it.end } + view.to
        val first = (starts.minOrNull() ?: view.from).coerceAtLeast(view.from)
        val start = first.atZone(zone).withMinute(0).withSecond(0).withNano(0).toInstant()
        val last = ends.max()
        val floor = last.atZone(zone).withMinute(0).withSecond(0).withNano(0)
        val end = (if (floor.toInstant() == last) floor else floor.plusHours(1)).toInstant()
        return start to end
    }
}

/** The last day answer without segments, shared by the home screen and kept for offline. */
object DayCache {
    private const val PREFS = "telemetry-day"
    private const val KEY_JSON = "json"

    fun read(context: Context): DayView? {
        val json = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_JSON, null) ?: return null
        return runCatching { DayModel.parse(json) }.getOrNull()
    }

    /** Fetches and stores today; null when offline or unpaired. Blocking IO. */
    fun refresh(context: Context): DayView? {
        val body = DeviceApi.get(context, "/v1/device/day") ?: return null
        val view = runCatching { DayModel.parse(body) }.getOrNull() ?: return null
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_JSON, body).apply()
        return view
    }

    /** Today with every record behind each chunk, for the details page. Not cached. Blocking IO. */
    fun details(context: Context): DayView? =
        DeviceApi.get(context, "/v1/device/day?segments=1")?.let { runCatching { DayModel.parse(it) }.getOrNull() }
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
