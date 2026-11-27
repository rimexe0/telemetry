package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

// Activity's Places: where the phone was on a day, as the panel's
// web/src/lib/places.ts draws it. Missing positions are missing: the line
// breaks where the phone recorded nothing for more than 15 minutes, and the
// list says so, except inside a stay, where the phone records nothing
// because it is standing still.

data class NearbyPlace(val name: String, val lat: Double, val lon: Double, val meters: Double)

data class PlaceStay(val from: Instant, val to: Instant, val lat: Double, val lon: Double, val home: Boolean, val nearby: List<NearbyPlace>)

data class HabitPlace(val habitId: Long, val habit: String, val name: String, val lat: Double, val lon: Double, val radiusM: Int)

/** One day of places: /v1/places/day, with the phone's own positions merged in. */
data class PlacesDay(
    val date: String,
    val zone: ZoneId,
    val from: Instant,
    val to: Instant,
    val positions: List<RuleFix>,
    val stays: List<PlaceStay>,
    val rules: List<HabitPlace>,
)

data class Span(val from: Instant, val to: Instant)

data class PlaceVisit(val from: Instant, val to: Instant, val place: HabitPlace)

data class PlaceItem(val from: Instant, val to: Instant, val kind: Kind, val text: String) {
    enum class Kind { HOME, STAY, VISIT, GAP }
}

object Places {
    /** The poorest position drawn, as the server's stays and habit rules use. */
    const val WORST_ACCURACY = 100.0
    /** The longest time between two positions the line still joins. */
    const val LONGEST_GAP_MS = 15 * 60_000L

    fun parse(json: String): PlacesDay? = runCatching {
        val root = JSONObject(json)
        val zone = runCatching { ZoneId.of(root.optString("timezone")) }.getOrDefault(ZoneId.of("UTC"))
        PlacesDay(
            date = root.getString("date"),
            zone = zone,
            from = Instant.parse(root.getString("from")),
            to = Instant.parse(root.getString("to")),
            positions = objects(root.optJSONArray("positions")).mapNotNull { item ->
                runCatching { RuleFix(Instant.parse(item.getString("at")), item.getDouble("lat"), item.getDouble("lon"), item.optDouble("accuracy", 0.0)) }.getOrNull()
            },
            stays = objects(root.optJSONArray("stays")).mapNotNull { item ->
                runCatching {
                    PlaceStay(Instant.parse(item.getString("from")), Instant.parse(item.getString("to")), item.getDouble("lat"), item.getDouble("lon"), item.optBoolean("home"),
                        objects(item.optJSONArray("nearby")).map { NearbyPlace(it.optString("name"), it.optDouble("lat"), it.optDouble("lon"), it.optDouble("meters")) }
                            .filter { it.name.isNotBlank() })
                }.getOrNull()
            },
            rules = objects(root.optJSONArray("rules")).mapNotNull { item ->
                runCatching { HabitPlace(item.getLong("habit_id"), item.optString("habit"), item.optString("name"), item.getDouble("lat"), item.getDouble("lon"), item.optInt("radius_m", 100)) }.getOrNull()
            },
        )
    }.getOrNull()

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }

    /**
     * The day from the server's answer with the phone's own positions added,
     * each counted once: a phone position within two seconds of one the
     * server has is the same position. Without an answer, the day is the
     * phone's positions and the habits' places it has saved, with no stays.
     */
    fun merge(server: PlacesDay?, date: String, zone: ZoneId, from: Instant, to: Instant, local: List<RuleFix>, rules: List<HabitPlace>): PlacesDay {
        val base = server ?: PlacesDay(date, zone, from, to, emptyList(), emptyList(), rules)
        val known = base.positions.map { it.at.toEpochMilli() }.sorted()
        val added = local.filter { fix ->
            val at = fix.at.toEpochMilli()
            !fix.at.isBefore(base.from) && fix.at.isBefore(base.to.coerceAtLeast(to)) && !near(known, at, 2_000)
        }
        // Today's day runs to now, which the phone knows better than an old answer.
        val end = if (server != null && to.isAfter(server.to) && server.date == date) to else base.to
        return base.copy(positions = (base.positions + added).sortedBy { it.at }, to = end)
    }

    private fun near(sorted: List<Long>, at: Long, within: Long): Boolean {
        var index = sorted.binarySearch(at)
        if (index >= 0) return true
        index = -index - 1
        return (index < sorted.size && sorted[index] - at <= within) || (index > 0 && at - sorted[index - 1] <= within)
    }

    /** The day's positions within 100 m, oldest first. */
    fun usable(positions: List<RuleFix>): List<RuleFix> = positions.filter { it.accuracy <= WORST_ACCURACY }.sortedBy { it.at }

    /** The path as lines, broken wherever positions are more than 15 minutes apart. */
    fun pathLines(points: List<RuleFix>): List<List<RuleFix>> {
        val lines = mutableListOf<List<RuleFix>>()
        var current = mutableListOf<RuleFix>()
        for (point in points) {
            val last = current.lastOrNull()
            if (last != null && point.at.toEpochMilli() - last.at.toEpochMilli() > LONGEST_GAP_MS) {
                lines += current
                current = mutableListOf()
            }
            current += point
        }
        if (current.isNotEmpty()) lines += current
        return lines
    }

    /** How near a stay's place a position must be to belong to it (the server's StayRadius). */
    const val STAY_RADIUS_M = 150.0

    /**
     * The path as lines, with no line inside a stay: the positions within a
     * stay's time and 150 m of its place stand at the stay's place, so GPS
     * jitter indoors draws no star around home, and the path runs only
     * between stays. Still broken wherever positions are more than 15
     * minutes apart.
     */
    fun pathLines(points: List<RuleFix>, stays: List<PlaceStay>): List<List<RuleFix>> {
        if (stays.isEmpty()) return pathLines(points)
        // The breaks come from the positions as they were, so a stay never makes a gap or hides one.
        return pathLines(points).map { line ->
            val placed = line.map { point ->
                val stay = stays.firstOrNull {
                    !point.at.isBefore(it.from) && !point.at.isAfter(it.to) && PlaceRule.meters(point.lat, point.lon, it.lat, it.lon) <= STAY_RADIUS_M
                }
                if (stay == null) point else RuleFix(point.at, stay.lat, stay.lon, point.accuracy)
            }
            // A run standing at one place is one point, so it draws no line.
            placed.filterIndexed { i, fix -> i == 0 || fix.lat != placed[i - 1].lat || fix.lon != placed[i - 1].lon }
        }
    }

    private fun without(span: Span, taken: List<Span>): List<Span> {
        var left = listOf(span)
        for (cut in taken) {
            left = left.flatMap { part ->
                if (!cut.to.isAfter(part.from) || !cut.from.isBefore(part.to)) listOf(part)
                else buildList {
                    if (cut.from.isAfter(part.from)) add(Span(part.from, cut.from))
                    if (cut.to.isBefore(part.to)) add(Span(cut.to, part.to))
                }
            }
        }
        return left
    }

    /** The stretches longer than 15 minutes with no position, from the day's start to its end, outside stays. */
    fun gaps(points: List<RuleFix>, stays: List<Span>, from: Instant, to: Instant): List<Span> {
        val edges = listOf(from) + points.map { it.at }.filter { it.isAfter(from) && it.isBefore(to) } + listOf(to)
        val gaps = mutableListOf<Span>()
        for (i in 1 until edges.size) {
            if (edges[i].toEpochMilli() - edges[i - 1].toEpochMilli() <= LONGEST_GAP_MS) continue
            for (part in without(Span(edges[i - 1], edges[i]), stays)) {
                if (part.to.toEpochMilli() - part.from.toEpochMilli() > LONGEST_GAP_MS) gaps += part
            }
        }
        return gaps
    }

    /** The times the positions were inside a habit's place: runs within its radius no more than 15 minutes apart. */
    fun visits(points: List<RuleFix>, rules: List<HabitPlace>): List<PlaceVisit> {
        val visits = mutableListOf<PlaceVisit>()
        for (place in rules) {
            var current: PlaceVisit? = null
            for (point in points) {
                val inside = PlaceRule.meters(point.lat, point.lon, place.lat, place.lon) <= place.radiusM
                val open = current
                if (inside && open != null && point.at.toEpochMilli() - open.to.toEpochMilli() <= LONGEST_GAP_MS) {
                    current = open.copy(to = point.at)
                    continue
                }
                if (open != null) visits += open
                current = if (inside) PlaceVisit(point.at, point.at, place) else null
            }
            current?.let { visits += it }
        }
        return visits.sortedBy { it.from }
    }

    private val clockFormat: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

    fun clock(at: Instant, zone: ZoneId): String = clockFormat.format(at.atZone(zone))

    /** "under 1 min", "23 min", "1 h 5 min". */
    fun duration(millis: Long): String {
        val minutes = Math.round(millis / 60_000.0)
        return when {
            minutes < 1 -> "under 1 min"
            minutes < 60 -> "$minutes min"
            minutes % 60 != 0L -> "${minutes / 60} h ${minutes % 60} min"
            else -> "${minutes / 60} h"
        }
    }

    /** A stay's place: the habit place it was at, else the nearest named place, else nothing. */
    fun stayName(stay: PlaceStay, visits: List<PlaceVisit>): String =
        visits.firstOrNull { !it.from.isAfter(stay.to) && !it.to.isBefore(stay.from) }?.place?.name ?: stay.nearby.firstOrNull()?.name.orEmpty()

    /** A stay's label on the map and in the list: its time span and place. */
    fun stayLabel(stay: PlaceStay, visits: List<PlaceVisit>, zone: ZoneId): String {
        val span = "${clock(stay.from, zone)}–${clock(stay.to, zone)}"
        if (stay.home) return "home $span"
        val name = stayName(stay, visits)
        return if (name.isNotEmpty()) "$span $name" else span
    }

    /**
     * The day in time order, in the panel's words:
     *   home 08:23–09:10
     *   08:17 Karantina İskelesi · 1 min
     *   09:15–09:40 Kahve Dünyası · 25 min
     *   no positions 10:20–11:05
     */
    fun list(day: PlacesDay): List<PlaceItem> {
        val zone = day.zone
        val points = usable(day.positions)
        val spans = day.stays.map { Span(it.from, it.to) }
        val visits = visits(points, day.rules)
        val items = mutableListOf<PlaceItem>()
        day.stays.forEachIndexed { i, stay ->
            val span = spans[i]
            val text = if (stay.home) stayLabel(stay, visits, zone) else "${stayLabel(stay, visits, zone)} · ${duration(span.to.toEpochMilli() - span.from.toEpochMilli())}"
            items += PlaceItem(span.from, span.to, if (stay.home) PlaceItem.Kind.HOME else PlaceItem.Kind.STAY, text)
        }
        for (visit in visits) {
            if (spans.any { !visit.from.isAfter(it.to) && !visit.to.isBefore(it.from) }) continue
            items += PlaceItem(visit.from, visit.to, PlaceItem.Kind.VISIT, "${clock(visit.from, zone)} ${visit.place.name} · ${duration(visit.to.toEpochMilli() - visit.from.toEpochMilli())}")
        }
        // A day that ends at midnight ends at 24:00; today's ends now.
        val ended = day.to.atZone(zone).toLocalDate().toString() != day.date
        for (gap in gaps(points, spans, day.from, day.to)) {
            val text = when {
                gap.to.isBefore(day.to) -> "no positions ${clock(gap.from, zone)}–${clock(gap.to, zone)}"
                ended -> "no positions ${clock(gap.from, zone)}–24:00"
                else -> "no positions since ${clock(gap.from, zone)}"
            }
            items += PlaceItem(gap.from, gap.to, PlaceItem.Kind.GAP, text)
        }
        return items.sortedWith(compareBy<PlaceItem> { it.from }.thenBy { it.to })
    }

    /** The habits' places from the saved habit list, for a day the server has not answered. */
    fun rulesFrom(list: HabitList?): List<HabitPlace> = list?.active.orEmpty().mapNotNull { habit ->
        val rule = habit.rule ?: return@mapNotNull null
        HabitPlace(habit.id, habit.name, rule.name, rule.lat, rule.lon, rule.radiusM)
    }
}
