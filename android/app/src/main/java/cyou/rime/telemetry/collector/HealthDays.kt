package cyou.rime.telemetry.collector

import android.content.Context
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.health.connect.client.request.AggregateRequest
import androidx.health.connect.client.request.ReadRecordsRequest
import androidx.health.connect.client.time.TimeRangeFilter
import org.json.JSONArray
import org.json.JSONObject
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

/** What Activity › Health shows: the last days, newest first, and what could not be read. */
data class HealthWeek(val days: List<HealthDay>, val missing: List<String>)

/**
 * Sleep, heart rate and steps per day, read on the phone from Health
 * Connect with the permissions the app already has, so the screen works
 * without the server. The last answer is kept, so it shows at once.
 */
object HealthDays {
    private const val PACKAGE = "com.google.android.apps.healthdata"
    private const val KEY = "health-week"
    const val DAYS = 14

    fun saved(context: Context): Pair<HealthWeek, Instant>? =
        AnswerStore.of(context).read(KEY)?.let { entry -> parse(entry.json)?.let { it to entry.at } }

    /** Reads the last days from Health Connect and keeps them. Null when Health Connect isn't there. */
    suspend fun refresh(context: Context, today: LocalDate = LocalDate.now(), zone: ZoneId = ZoneId.systemDefault()): HealthWeek? {
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
        val first = today.minusDays((DAYS - 1).toLong())
        val sleeps = if (!sleep) emptyList() else runCatching {
            client.readRecords(ReadRecordsRequest(SleepSessionRecord::class,
                TimeRangeFilter.between(first.minusDays(1).atStartOfDay(zone).toInstant(), today.plusDays(1).atStartOfDay(zone).toInstant()), pageSize = 200)).records
        }.getOrDefault(emptyList())
        val days = (0 until DAYS).map { back ->
            val day = today.minusDays(back.toLong())
            val from = day.atStartOfDay(zone).toInstant()
            val to = day.plusDays(1).atStartOfDay(zone).toInstant()
            val metrics = buildSet {
                if (steps) add(StepsRecord.COUNT_TOTAL)
                if (heart) { add(HeartRateRecord.BPM_MIN); add(HeartRateRecord.BPM_AVG); add(HeartRateRecord.BPM_MAX) }
            }
            val result = if (metrics.isEmpty()) null else runCatching { client.aggregate(AggregateRequest(metrics, TimeRangeFilter.between(from, to))) }.getOrNull()
            val slept = sleeps.filter { !it.endTime.isBefore(from) && it.endTime.isBefore(to) }
            HealthDay(
                date = day.toString(),
                steps = result?.get(StepsRecord.COUNT_TOTAL),
                heartMin = result?.get(HeartRateRecord.BPM_MIN),
                heartAvg = result?.get(HeartRateRecord.BPM_AVG),
                heartMax = result?.get(HeartRateRecord.BPM_MAX),
                sleepMinutes = if (!sleep || slept.isEmpty()) null else slept.sumOf { java.time.Duration.between(it.startTime, it.endTime).toMinutes() },
            )
        }
        val week = HealthWeek(days, missing)
        AnswerStore.of(context).put(KEY, toJson(week))
        return week
    }

    fun toJson(week: HealthWeek): String = JSONObject()
        .put("missing", JSONArray(week.missing))
        .put("days", JSONArray(week.days.map { day ->
            JSONObject().put("date", day.date).put("steps", day.steps ?: JSONObject.NULL)
                .put("heart_min", day.heartMin ?: JSONObject.NULL).put("heart_avg", day.heartAvg ?: JSONObject.NULL).put("heart_max", day.heartMax ?: JSONObject.NULL)
                .put("sleep_minutes", day.sleepMinutes ?: JSONObject.NULL)
        })).toString()

    fun parse(json: String): HealthWeek? = runCatching {
        val root = JSONObject(json)
        fun JSONObject.long(name: String): Long? = if (isNull(name) || !has(name)) null else optLong(name)
        val days = root.getJSONArray("days")
        val missing = root.optJSONArray("missing")
        HealthWeek(
            (0 until days.length()).map { days.getJSONObject(it) }.map {
                HealthDay(it.getString("date"), it.long("steps"), it.long("heart_min"), it.long("heart_avg"), it.long("heart_max"), it.long("sleep_minutes"))
            },
            if (missing == null) emptyList() else (0 until missing.length()).map { missing.getString(it) },
        )
    }.getOrNull()

    /** "7 h 20 min", or "no sleep recorded". */
    fun sleepText(minutes: Long?): String = if (minutes == null) "no sleep recorded" else ActivityText.minutes(minutes.toDouble())

    fun heartText(day: HealthDay): String =
        if (day.heartAvg == null) "no heart rate recorded" else "${day.heartAvg} bpm average · ${day.heartMin}–${day.heartMax}"

    fun stepsText(steps: Long?): String = if (steps == null) "no steps recorded" else "%,d steps".format(java.util.Locale.UK, steps)
}
