package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId

/** Time on one app or site in a stretch of use, named as the phone names it (WhatsApp, not com.whatsapp). */
data class DayItem(val name: String, val label: String, val minutes: Double)

/** A stretch of continuous use of one device (/v1/device/day's chunks). */
data class DayChunk(val device: String, val start: Instant, val end: Instant, val kind: String, val task: String?, val activeMinutes: Double, val distractionMinutes: Double, val items: List<DayItem>)

/** A stretch a device sent nothing: unknown, never empty. */
data class DayGap(val device: String, val start: Instant, val end: Instant)

/** One day as /v1/device/day gives it, without segments. */
data class DayView(
    val date: String,
    val from: Instant,
    val to: Instant,
    val distractionMinutes: Double,
    val distractionByDevice: Map<String, Double>,
    val chunks: List<DayChunk>,
    val gaps: List<DayGap>,
)

/** A line of the Timeline: a stretch of use, or a stretch with no data. */
sealed interface TimelineRow {
    val start: Instant
    val end: Instant

    data class Use(override val start: Instant, override val end: Instant, val minutes: Double, val distraction: Boolean, val items: List<DayItem>) : TimelineRow
    data class NoData(override val start: Instant, override val end: Instant) : TimelineRow
}

data class TimelineDevice(val device: String, val activeMinutes: Double, val distractionMinutes: Double, val noDataMinutes: Double, val rows: List<TimelineRow>)

object DayModel {
    fun parse(json: String): DayView? = runCatching {
        val root = JSONObject(json)
        val day = root.getJSONObject("day")
        DayView(
            date = root.getString("date"),
            from = Instant.parse(root.getString("from")),
            to = Instant.parse(root.getString("to")),
            distractionMinutes = day.optDouble("distraction_minutes", 0.0),
            distractionByDevice = objects(day.optJSONArray("distraction_by_device")).associate { it.optString("device") to it.optDouble("minutes", 0.0) },
            chunks = objects(day.optJSONArray("chunks")).mapNotNull { chunk ->
                runCatching {
                    DayChunk(
                        device = chunk.getString("device"),
                        start = Instant.parse(chunk.getString("start")),
                        end = Instant.parse(chunk.getString("end")),
                        kind = chunk.optString("kind"),
                        task = chunk.optString("task").ifBlank { null },
                        activeMinutes = chunk.optDouble("active_minutes", 0.0),
                        distractionMinutes = chunk.optDouble("distraction_minutes", 0.0),
                        items = objects(chunk.optJSONArray("items")).map { item ->
                            DayItem(item.optString("app_name").ifBlank { item.optString("name") }, item.optString("label"), item.optDouble("minutes", 0.0))
                        },
                    )
                }.getOrNull()
            },
            gaps = objects(day.optJSONArray("no_data")).mapNotNull {
                runCatching { DayGap(it.getString("device"), Instant.parse(it.getString("start")), Instant.parse(it.getString("end"))) }.getOrNull()
            },
        )
    }.getOrNull()

    /** Mac first, then the phone, then any other device, each with its stretches and its missing ones in time order. */
    fun timeline(view: DayView): List<TimelineDevice> {
        val devices = (view.chunks.map { it.device } + view.gaps.map { it.device }).distinct()
            .sortedWith(compareBy<String> { when (it) { "mac" -> 0; "phone" -> 1; else -> 2 } }.thenBy { it })
        return devices.map { device ->
            val chunks = view.chunks.filter { it.device == device }
            val gaps = view.gaps.filter { it.device == device }
            val rows = chunks.map { chunk ->
                TimelineRow.Use(chunk.start, chunk.end, chunk.activeMinutes, chunk.kind == "distraction" || chunk.distractionMinutes >= chunk.activeMinutes / 2 && chunk.distractionMinutes > 0,
                    chunk.items.filter { it.minutes > 0 }.sortedByDescending { it.minutes })
            } + gaps.map { TimelineRow.NoData(it.start, it.end) }
            TimelineDevice(
                device = device,
                activeMinutes = chunks.sumOf { it.activeMinutes },
                distractionMinutes = view.distractionByDevice[device] ?: chunks.sumOf { it.distractionMinutes },
                noDataMinutes = gaps.sumOf { (it.end.toEpochMilli() - it.start.toEpochMilli()) / 60_000.0 },
                rows = rows.sortedWith(compareBy<TimelineRow> { it.start }.thenBy { it.end }),
            )
        }
    }

    fun span(row: TimelineRow, zone: ZoneId): String = "${Places.clock(row.start, zone)}–${Places.clock(row.end, zone)}"

    /** A stretch's apps and sites in one line: "WhatsApp 12 min · X 8 min", the first few only. */
    fun itemsLine(items: List<DayItem>, limit: Int = 3): String {
        val shown = items.take(limit).joinToString(" · ") { "${it.name} ${ActivityText.minutes(it.minutes)}" }
        return if (items.size > limit) "$shown · ${items.size - limit} more" else shown
    }

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }
}

/** The panel's words for time and devices (web/src/lib/activity.ts). */
object ActivityText {
    /** "under 1 min", "41 min", "1 h 12 min", "2 h". */
    fun minutes(minutes: Double): String {
        val total = Math.round(minutes).toInt()
        if (minutes > 0 && total < 1) return "under 1 min"
        if (total < 60) return "$total min"
        val rest = total % 60
        return if (rest != 0) "${total / 60} h $rest min" else "${total / 60} h"
    }

    fun deviceName(device: String): String = when (device) {
        "mac" -> "Mac"
        "phone" -> "Phone"
        else -> device
    }
}

/** One app, site or label with its time (/v1/activity/summary). */
data class Usage(val name: String, val label: String, val minutes: Double)

data class SummaryDevice(val device: String, val activeMinutes: Double, val noDataMinutes: Double, val labels: List<Usage>, val apps: List<Usage>, val sites: List<Usage>)

data class Insight(val key: String, val text: String, val detail: String)

/** What the panel's Apps view shows. */
data class ActivitySummary(val insights: List<Insight>, val devices: List<SummaryDevice>)

object SummaryModel {
    fun parse(json: String): ActivitySummary? = runCatching {
        val root = JSONObject(json)
        val names = root.optJSONObject("app_names")
        fun named(text: String, raw: List<String>): String = raw.fold(text) { acc, package_ ->
            val name = names?.optString(package_).orEmpty()
            if (name.isBlank()) acc else acc.replace(package_, name)
        }
        val insights = objects(root.optJSONArray("insights")).map { item ->
            val raw = item.optJSONArray("names")?.let { array -> (0 until array.length()).map { array.optString(it) }.filter { it.isNotBlank() } }.orEmpty()
            Insight(item.optString("key"), named(item.optString("text"), raw), named(item.optString("detail"), raw))
        }
        fun usage(array: JSONArray?) = objects(array).map { Usage(it.optString("app_name").ifBlank { it.optString("name") }, it.optString("label"), it.optDouble("minutes", 0.0)) }
        ActivitySummary(
            insights = insights,
            devices = objects(root.optJSONArray("devices")).map {
                SummaryDevice(it.optString("device"), it.optDouble("active_minutes", 0.0), it.optDouble("no_data_minutes", 0.0),
                    usage(it.optJSONArray("labels")), usage(it.optJSONArray("apps")), usage(it.optJSONArray("sites")))
            },
        )
    }.getOrNull()

    /** "5 h 2 min active · no data for 1 h", the missing part only from 15 minutes. */
    fun deviceNote(device: SummaryDevice): String =
        "${ActivityText.minutes(device.activeMinutes)} active" + if (device.noDataMinutes >= 15) " · no data for ${ActivityText.minutes(device.noDataMinutes)}" else ""

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }
}
