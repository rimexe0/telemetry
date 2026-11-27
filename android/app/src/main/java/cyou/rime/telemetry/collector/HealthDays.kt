package cyou.rime.telemetry.collector

import android.content.Context
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.health.connect.client.request.AggregateGroupByDurationRequest
import androidx.health.connect.client.request.AggregateRequest
import androidx.health.connect.client.request.ReadRecordsRequest
import androidx.health.connect.client.time.TimeRangeFilter
import org.json.JSONArray
import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/** One day's health numbers; null where nothing was recorded or it can't be read. */
data class HealthDay(
    val date: String,
    val steps: Long?,
    val heartMin: Long?,
    val heartAvg: Long?,
    val heartMax: Long?,
    /** Sleep that ended this day, in minutes. */
    val sleepMinutes: Long?,
)

/** A heart rate in the day: the average of a ten-minute stretch, from [minute] after midnight. */
data class HeartPoint(val minute: Int, val bpm: Long)

/** What Activity › Health shows for one day: its numbers, steps per hour, heart rate through the day, and what could not be read. */
data class HealthDayView(val day: HealthDay, val stepsByHour: List<Long?>, val heart: List<HeartPoint>, val missing: List<String>)

/**
 * Sleep, heart rate and steps for one day, read on the phone from Health
 * Connect with the permissions the app already has, so the screen works
 * without the server. Each day read is kept, so it shows at once.
 */
object HealthDays {
    private const val PACKAGE = "com.google.android.apps.healthdata"
    /** Heart rate is drawn as the average of each stretch this long. */
    private const val HEART_STEP_MINUTES = 10

    /** Kept per day, so old days are dropped with the other per-day answers. */
    fun key(day: String) = "health-$day"

    fun saved(context: Context, day: String): Pair<HealthDayView, Instant>? =
        AnswerStore.of(context).read(key(day))?.let { entry -> parse(entry.json)?.let { it to entry.at } }

    /** Reads [date] from Health Connect and keeps it. Null when Health Connect isn't there. */
    suspend fun read(context: Context, date: LocalDate, zone: ZoneId = ZoneId.systemDefault()): HealthDayView? {
        val available = runCatching { HealthConnectClient.getSdkStatus(context, PACKAGE) == HealthConnectClient.SDK_AVAILABLE }.getOrDefault(false)
        if (!available) return null
        val client = runCatching { HealthConnectClient.getOrCreate(context, PACKAGE) }.getOrNull() ?: return null
        val granted = runCatching { client.permissionController.getGrantedPermissions() }.getOrNull() ?: return null
        val steps = HealthPermission.getReadPermission(StepsRecord::class) in granted
        val heart = HealthPermission.getReadPermission(HeartRateRecord::class) in granted
        val sleep = HealthPermission.getReadPermission(SleepSessionRecord::class) in granted
        val missing = buildList {
            if (!sleep) add("sleep")
            if (!heart) add("heart rate")
            if (!steps) add("steps")
        }
        val from = date.atStartOfDay(zone).toInstant()
        val to = date.plusDays(1).atStartOfDay(zone).toInstant()
        val metrics = buildSet {
            if (steps) add(StepsRecord.COUNT_TOTAL)
            if (heart) { add(HeartRateRecord.BPM_MIN); add(HeartRateRecord.BPM_AVG); add(HeartRateRecord.BPM_MAX) }
        }
        val result = if (metrics.isEmpty()) null else runCatching { client.aggregate(AggregateRequest(metrics, TimeRangeFilter.between(from, to))) }.getOrNull()
        val slept = if (!sleep) emptyList() else runCatching {
            client.readRecords(ReadRecordsRequest(SleepSessionRecord::class, TimeRangeFilter.between(from.minus(Duration.ofDays(1)), to), pageSize = 50)).records
        }.getOrDefault(emptyList()).filter { !it.endTime.isBefore(from) && it.endTime.isBefore(to) }
        val hours = MutableList<Long?>(24) { null }
        if (steps) runCatching {
            client.aggregateGroupByDuration(AggregateGroupByDurationRequest(setOf(StepsRecord.COUNT_TOTAL), TimeRangeFilter.between(from, to), Duration.ofHours(1)))
        }.getOrNull()?.forEach { slice ->
            val count = slice.result[StepsRecord.COUNT_TOTAL] ?: return@forEach
            val hour = slice.startTime.atZone(zone).hour
            hours[hour] = (hours[hour] ?: 0L) + count
        }
        val samples = mutableListOf<Pair<Instant, Long>>()
        if (heart) runCatching {
            var token: String? = null
            do {
                val page = client.readRecords(ReadRecordsRequest(HeartRateRecord::class, TimeRangeFilter.between(from, to), pageSize = 1000, pageToken = token))
                page.records.forEach { record -> record.samples.forEach { samples += it.time to it.beatsPerMinute } }
                token = page.pageToken
            } while (token != null && samples.size < 200_000)
        }
        val view = HealthDayView(
            day = HealthDay(
                date = date.toString(),
                steps = result?.get(StepsRecord.COUNT_TOTAL),
                heartMin = result?.get(HeartRateRecord.BPM_MIN),
                heartAvg = result?.get(HeartRateRecord.BPM_AVG),
                heartMax = result?.get(HeartRateRecord.BPM_MAX),
                sleepMinutes = if (!sleep || slept.isEmpty()) null else slept.sumOf { Duration.between(it.startTime, it.endTime).toMinutes() },
            ),
            stepsByHour = hours,
            heart = heartLine(samples, from, to),
            missing = missing,
        )
        AnswerStore.of(context).put(key(date.toString()), toJson(view))
        return view
    }

    /** The day's heart rate as the average of each ten-minute stretch that has any, in time order. */
    fun heartLine(samples: List<Pair<Instant, Long>>, from: Instant, to: Instant): List<HeartPoint> =
        samples.filter { !it.first.isBefore(from) && it.first.isBefore(to) }
            .groupBy { (Duration.between(from, it.first).toMinutes() / HEART_STEP_MINUTES).toInt() }
            .toSortedMap()
            .map { (step, group) -> HeartPoint(step * HEART_STEP_MINUTES, Math.round(group.map { it.second }.average())) }

    fun toJson(view: HealthDayView): String {
        val day = view.day
        return JSONObject().put("date", day.date).put("steps", day.steps ?: JSONObject.NULL)
            .put("heart_min", day.heartMin ?: JSONObject.NULL).put("heart_avg", day.heartAvg ?: JSONObject.NULL).put("heart_max", day.heartMax ?: JSONObject.NULL)
            .put("sleep_minutes", day.sleepMinutes ?: JSONObject.NULL)
            .put("missing", JSONArray(view.missing))
            .put("steps_by_hour", JSONArray(view.stepsByHour.map { it ?: JSONObject.NULL }))
            .put("heart", JSONArray(view.heart.map { JSONArray().put(it.minute).put(it.bpm) }))
            .toString()
    }

    fun parse(json: String): HealthDayView? = runCatching {
        val root = JSONObject(json)
        fun JSONObject.long(name: String): Long? = if (isNull(name) || !has(name)) null else optLong(name)
        val missing = root.optJSONArray("missing")
        val hours = root.optJSONArray("steps_by_hour")
        val heart = root.optJSONArray("heart")
        HealthDayView(
            day = HealthDay(root.getString("date"), root.long("steps"), root.long("heart_min"), root.long("heart_avg"), root.long("heart_max"), root.long("sleep_minutes")),
            stepsByHour = (0 until 24).map { hour -> if (hours == null || hour >= hours.length() || hours.isNull(hour)) null else hours.optLong(hour) },
            heart = if (heart == null) emptyList() else (0 until heart.length()).mapNotNull { heart.optJSONArray(it) }.map { HeartPoint(it.optInt(0), it.optLong(1)) },
            missing = if (missing == null) emptyList() else (0 until missing.length()).map { missing.getString(it) },
        )
    }.getOrNull()

    /** "7 h 20 min", or "no sleep recorded". */
    fun sleepText(minutes: Long?): String = if (minutes == null) "no sleep recorded" else ActivityText.minutes(minutes.toDouble())

    fun heartText(day: HealthDay): String =
        if (day.heartAvg == null) "no heart rate recorded" else "${day.heartAvg} bpm average · ${day.heartMin}–${day.heartMax}"

    fun stepsText(steps: Long?): String = if (steps == null) "no steps recorded" else "%,d steps".format(java.util.Locale.UK, steps)
}
