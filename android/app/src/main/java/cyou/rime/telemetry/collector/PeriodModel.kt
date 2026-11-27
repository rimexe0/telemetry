package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.LocalDate

// Activity's weeks, months and years: the server's one row per day
// (/v1/activity/days), kept on the phone, and the averages per day the
// views show. A day without data is left out of an average, never zero.

data class RowDevice(val device: String, val activeMinutes: Double, val distractionMinutes: Double, val noDataMinutes: Double, val reported: Boolean)

data class RowUsage(val device: String, val name: String, val label: String, val minutes: Double)

data class RowStay(val from: Instant, val to: Instant, val lat: Double, val lon: Double, val home: Boolean, val name: String)

/** One day as /v1/activity/days gives it. */
data class DayRow(
    val date: String,
    val final: Boolean,
    val version: String,
    val minutes: Double,
    val distractionMinutes: Double,
    val devices: List<RowDevice>,
    val agentMinutes: Double?,
    val apps: List<RowUsage>,
    val sites: List<RowUsage>,
    /** The day in ten-minute parts, as the server's activity.DayCells. */
    val cells: String,
    val positions: Int,
    val outsideMinutes: Double?,
    val distanceKm: Double?,
    val stays: List<RowStay>,
) {
    /** Some device sent something this day. */
    val reported: Boolean get() = devices.any { it.reported }
}

/** One part of a day's bar. */
enum class Cell { DISTRACTION, ACTIVE, NO_DATA, IDLE, LATER }

/** An app or site over a period: its average per day on the days its device sent anything. */
data class PeriodUsage(val name: String, val label: String, val perDay: Double)

data class PeriodDevice(val device: String, val active: Average?, val apps: List<PeriodUsage>, val sites: List<PeriodUsage>)

/** What Apps and Timeline show for a period. */
data class PeriodActivity(
    val days: List<LocalDate>,
    /** Each day's row, null for a day the phone has none of. */
    val rows: List<DayRow?>,
    val distraction: Average?,
    val agents: Average?,
    val devices: List<PeriodDevice>,
)

/** A place stayed at over a period: on how many days, and for how long in all. */
data class PeriodPlace(val name: String, val home: Boolean, val lat: Double, val lon: Double, val days: Int, val minutes: Double)

data class PeriodPlaces(val away: Average?, val distanceKm: Average?, val places: List<PeriodPlace>)

/** What Health shows for a period: sleep, heart rate and steps per day, each over the days that have it. */
data class PeriodHealth(val days: List<LocalDate>, val rows: List<HealthDay?>, val sleep: Average?, val heart: Average?, val steps: Average?, val missing: List<String>)

object PeriodModel {
    fun parse(json: String): Pair<String, List<DayRow>>? = runCatching {
        val root = JSONObject(json)
        val version = root.getString("version")
        version to objects(root.optJSONArray("days")).mapNotNull { row(it, version) }
    }.getOrNull()

    /** One row from the server's answer, or as kept on the phone with its labels' [version]. */
    fun row(item: JSONObject, version: String = item.optString("version")): DayRow? = runCatching {
        fun JSONObject.double(name: String): Double? = if (!has(name) || isNull(name)) null else optDouble(name)
        fun usage(array: JSONArray?) = objects(array).map {
            RowUsage(it.optString("device"), it.optString("app_name").ifBlank { it.optString("name") }, it.optString("label"), it.optDouble("minutes", 0.0))
        }
        DayRow(
            date = LocalDate.parse(item.getString("date")).toString(),
            final = item.optBoolean("final"),
            version = version,
            minutes = item.optDouble("minutes", 0.0),
            distractionMinutes = item.optDouble("distraction_minutes", 0.0),
            devices = objects(item.optJSONArray("devices")).map {
                RowDevice(it.optString("device"), it.optDouble("active_minutes", 0.0), it.optDouble("distraction_minutes", 0.0), it.optDouble("no_data_minutes", 0.0), it.optBoolean("reported"))
            },
            agentMinutes = item.double("agent_minutes"),
            apps = usage(item.optJSONArray("apps")),
            sites = usage(item.optJSONArray("sites")),
            cells = item.optString("cells"),
            positions = item.optInt("positions"),
            outsideMinutes = item.double("outside_minutes"),
            distanceKm = item.double("distance_km"),
            stays = objects(item.optJSONArray("stays")).mapNotNull {
                runCatching {
                    RowStay(Instant.parse(it.getString("from")), Instant.parse(it.getString("to")), it.getDouble("lat"), it.getDouble("lon"), it.optBoolean("home"), it.optString("name"))
                }.getOrNull()
            },
        )
    }.getOrNull()

    /** A row as the phone keeps it: the server's own fields, with the labels' version. */
    fun keep(item: JSONObject, version: String): JSONObject = JSONObject(item.toString()).put("version", version)

    fun cells(row: DayRow): List<Cell> = row.cells.map {
        when (it) {
            'd' -> Cell.DISTRACTION
            'a' -> Cell.ACTIVE
            'n' -> Cell.NO_DATA
            '_' -> Cell.LATER
            else -> Cell.IDLE
        }
    }

    /**
     * Apps and Timeline for [days]: distraction per day over the days any
     * device sent something; each device's screen time, apps and sites over
     * the days it sent something; agent time over the days it could be read.
     */
    fun activity(days: List<LocalDate>, byDate: Map<String, DayRow>, limit: Int = 10): PeriodActivity {
        val rows = days.map { byDate[it.toString()] }
        val known = rows.filterNotNull()
        val reported = known.filter { it.reported }
        val devices = listOf("mac", "phone").mapNotNull { device ->
            val sent = known.filter { row -> row.devices.any { it.device == device && it.reported } }
            if (sent.isEmpty()) return@mapNotNull null
            fun ranked(pick: (DayRow) -> List<RowUsage>): List<PeriodUsage> =
                sent.flatMap { row -> pick(row).filter { it.device == device } }
                    .groupBy { it.name }
                    .map { (name, items) -> PeriodUsage(name, items.first().label, items.sumOf { it.minutes } / sent.size) }
                    .sortedWith(compareByDescending<PeriodUsage> { it.perDay }.thenBy { it.name })
                    .take(limit)
            PeriodDevice(device, Average.of(sent.map { row -> row.devices.first { it.device == device }.activeMinutes }), ranked { it.apps }, ranked { it.sites })
        }
        return PeriodActivity(
            days = days,
            rows = rows,
            distraction = Average.of(reported.map { it.distractionMinutes }),
            agents = Average.of(known.filter { it.reported || (it.agentMinutes ?: 0.0) > 0 }.map { it.agentMinutes }),
            devices = devices,
        )
    }

    /**
     * Places for [days]: time away from home and distance moved per day over
     * the days with positions, and the places stayed at most, by days and
     * time. A stay's place is the habit place it was in, else its nearest
     * named place, else the spot itself; stays within 150 m of each other are
     * one place.
     */
    fun places(days: List<LocalDate>, byDate: Map<String, DayRow>, rules: List<HabitPlace>, limit: Int = 8): PeriodPlaces {
        val rows = days.mapNotNull { byDate[it.toString()] }
        val located = rows.filter { it.positions > 0 }
        data class Group(val name: String, val home: Boolean, val lat: Double, val lon: Double, val dates: MutableSet<String> = mutableSetOf(), var minutes: Double = 0.0)
        val groups = mutableListOf<Group>()
        for (row in located) for (stay in row.stays) {
            val rule = rules.firstOrNull { PlaceRule.meters(stay.lat, stay.lon, it.lat, it.lon) <= it.radiusM }
            val name = when {
                stay.home -> "home"
                rule != null -> rule.name
                else -> stay.name
            }
            val group = groups.firstOrNull { it.home == stay.home && (if (name.isNotEmpty()) it.name == name else it.name.isEmpty() && PlaceRule.meters(it.lat, it.lon, stay.lat, stay.lon) <= Places.STAY_RADIUS_M) }
                ?: Group(name, stay.home, stay.lat, stay.lon).also { groups += it }
            group.dates += row.date
            group.minutes += (stay.to.toEpochMilli() - stay.from.toEpochMilli()) / 60_000.0
        }
        return PeriodPlaces(
            away = Average.of(located.map { it.outsideMinutes }),
            distanceKm = Average.of(located.map { it.distanceKm }),
            places = groups.map { PeriodPlace(it.name, it.home, it.lat, it.lon, it.dates.size, it.minutes) }
                .sortedWith(compareByDescending<PeriodPlace> { it.days }.thenByDescending { it.minutes })
                .take(limit),
        )
    }

    fun health(days: List<LocalDate>, byDate: Map<String, HealthDay>, missing: List<String>): PeriodHealth {
        val rows = days.map { byDate[it.toString()] }
        return PeriodHealth(days, rows, Average.of(rows.map { it?.sleepMinutes?.toDouble() }), Average.of(rows.map { it?.heartAvg?.toDouble() }),
            Average.of(rows.map { it?.steps?.toDouble() }), missing)
    }

    /** A place's line: "Kahve Dünyası · 5 days · 3 h 20 min"; an unnamed one by where it is. */
    fun placeLine(place: PeriodPlace): String {
        val name = place.name.ifEmpty { "%.4f, %.4f".format(java.util.Locale.UK, place.lat, place.lon) }
        return "$name · ${if (place.days == 1) "1 day" else "${place.days} days"} · ${ActivityText.minutes(place.minutes)}"
    }

    /** "4.2 km a day". */
    fun kmPerDay(average: Average?): String = if (average == null) "no data" else "%.1f km a day".format(java.util.Locale.UK, average.perDay)

    /**
     * The days of [days] to ask the server for: those without a row, those
     * not final yet, and those counted with labels other than [version].
     */
    fun stale(days: List<LocalDate>, byDate: Map<String, DayRow>, version: String?): List<LocalDate> = days.filter { day ->
        val row = byDate[day.toString()]
        row == null || !row.final || (version != null && row.version != version)
    }

    /** [days] (oldest first) as runs of consecutive days, each at most [most] long, so one request covers each. */
    fun ranges(days: List<LocalDate>, most: Int = 62): List<Pair<LocalDate, LocalDate>> {
        val ranges = mutableListOf<Pair<LocalDate, LocalDate>>()
        var start: LocalDate? = null
        var end: LocalDate? = null
        var length = 0
        for (day in days) {
            if (start != null && end != null && day == end.plusDays(1) && length < most) {
                end = day
                length++
                continue
            }
            if (start != null && end != null) ranges += start to end
            start = day
            end = day
            length = 1
        }
        if (start != null && end != null) ranges += start to end
        return ranges
    }

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }
}
