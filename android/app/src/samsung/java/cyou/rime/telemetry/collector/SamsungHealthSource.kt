package cyou.rime.telemetry.collector

import android.app.Activity
import android.content.Context
import android.os.Looper
import com.samsung.android.sdk.health.data.HealthDataService
import com.samsung.android.sdk.health.data.HealthDataStore
import com.samsung.android.sdk.health.data.data.HealthDataPoint
import com.samsung.android.sdk.health.data.permission.AccessType
import com.samsung.android.sdk.health.data.permission.Permission
import com.samsung.android.sdk.health.data.request.DataType
import com.samsung.android.sdk.health.data.request.DataTypes
import com.samsung.android.sdk.health.data.request.InstantTimeFilter
import com.samsung.android.sdk.health.data.request.LocalTimeFilter
import com.samsung.android.sdk.health.data.request.LocalTimeGroup
import com.samsung.android.sdk.health.data.request.LocalTimeGroupUnit
import com.samsung.android.sdk.health.data.request.Ordering
import com.samsung.android.sdk.health.data.request.ReadDataRequest
import java.time.Instant
import java.time.LocalDateTime
import java.time.ZoneId
import java.util.concurrent.TimeUnit
import org.json.JSONArray
import org.json.JSONObject

/**
 * Reads Samsung Health through Samsung's Health Data SDK, read-only. Samsung
 * Health must have "Developer Mode for Data Read" on (Settings → About Samsung
 * Health, tap the version) while this app is not registered with Samsung.
 */
object SamsungHealthSource {
    const val SUPPORTED = true
    private const val PACKAGE = "com.sec.android.app.shealth"
    private const val PAGE_SIZE = 1_000
    private const val MAX_PAGES = 50
    private const val TIMEOUT_SECONDS = 30L

    private val permissions = setOf(
        Permission.of(DataTypes.HEART_RATE, AccessType.READ),
        Permission.of(DataTypes.EXERCISE, AccessType.READ),
        Permission.of(DataTypes.EXERCISE_LOCATION, AccessType.READ),
        Permission.of(DataTypes.SLEEP, AccessType.READ),
        Permission.of(DataTypes.STEPS, AccessType.READ),
    )

    fun installed(context: Context): Boolean =
        runCatching { context.packageManager.getPackageInfo(PACKAGE, 0) }.isSuccess

    private fun store(context: Context): HealthDataStore = HealthDataService.getStore(context.applicationContext)

    /** Asks Samsung Health for read access; done gets whether everything was allowed. */
    fun request(activity: Activity, done: (Boolean) -> Unit) {
        runCatching {
            store(activity).requestPermissionsAsync(permissions, activity).setCallback(Looper.getMainLooper(),
                { granted -> remember(activity, granted.containsAll(permissions)); done(granted.containsAll(permissions)) },
                { done(false) })
        }.onFailure { done(false) }
    }

    /** Reads what is new or changed and queues it. Blocking IO. Returns a state for the status report. */
    fun collect(context: Context, queue: HealthQueue, deviceId: String): String {
        if (!installed(context)) return "not_installed"
        val store = store(context)
        val granted = runCatching { store.getGrantedPermissionsAsync(permissions).get(TIMEOUT_SECONDS, TimeUnit.SECONDS) }
            .getOrElse { return "error:" + describe(it) }
        remember(context, granted.containsAll(permissions))
        if (!granted.containsAll(permissions)) return "permission_required"
        val prefs = context.getSharedPreferences(SamsungHealthRecords.PREFS, Context.MODE_PRIVATE)
        val now = Instant.now()
        val from = now.minus(if (prefs.getBoolean(SamsungHealthRecords.KEY_BACKFILLED, false)) SamsungHealthRecords.WINDOW else SamsungHealthRecords.BACKFILL)
        val records = runCatching {
            heartRate(store, from, now) + exercise(store, from, now) + sleep(store, from, now) + steps(store, from, now)
        }.getOrElse { return "error:" + describe(it) }
        val sent = JSONObject(prefs.getString(SamsungHealthRecords.KEY_SENT, "{}") ?: "{}")
        val (fresh, remembered) = SamsungHealthRecords.changed(records, sent, now)
        if (fresh.isNotEmpty()) queue.enqueue(SamsungHealthRecords.observations(deviceId, fresh, now))
        prefs.edit().putString(SamsungHealthRecords.KEY_SENT, remembered.toString()).putBoolean(SamsungHealthRecords.KEY_BACKFILLED, true).apply()
        return if (fresh.isEmpty()) "unchanged" else "ok:${fresh.size}"
    }

    private fun remember(context: Context, granted: Boolean) {
        context.getSharedPreferences(SamsungHealthRecords.PREFS, Context.MODE_PRIVATE).edit().putBoolean(SamsungHealthRecords.KEY_GRANTED, granted).apply()
    }

    /** Whether the last check found read access, for the setup screen. */
    fun grantedLastTime(context: Context): Boolean =
        context.getSharedPreferences(SamsungHealthRecords.PREFS, Context.MODE_PRIVATE).getBoolean(SamsungHealthRecords.KEY_GRANTED, false)

    private fun describe(error: Throwable): String = (error.javaClass.simpleName + ":" + (error.message ?: "")).take(160)

    private fun read(store: HealthDataStore, builder: () -> ReadDataRequest.DualTimeBuilder<HealthDataPoint>): List<HealthDataPoint> {
        val result = mutableListOf<HealthDataPoint>()
        var token: String? = null
        repeat(MAX_PAGES) {
            val request = builder().setOrdering(Ordering.ASC).setPageSize(PAGE_SIZE)
            token?.let { request.setPageToken(it) }
            val response = store.readDataAsync(request.build()).get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
            result += response.dataList
            token = response.pageToken
            if (token.isNullOrBlank()) return result
        }
        return result
    }

    private fun source(point: HealthDataPoint): JSONObject =
        JSONObject().put("app_id", point.dataSource?.appId ?: JSONObject.NULL).put("device_id", point.dataSource?.deviceId ?: JSONObject.NULL)

    private fun heartRate(store: HealthDataStore, from: Instant, to: Instant): List<SamsungRecord> =
        read(store) { DataTypes.HEART_RATE.readDataRequestBuilder.setInstantTimeFilter(InstantTimeFilter.of(from, to)) }.map { point ->
            val series = JSONArray()
            point.getValue(DataType.HeartRateType.SERIES_DATA)?.forEach { value ->
                series.put(JSONObject().put("start", value.startTime.toString()).put("end", value.endTime.toString())
                    .put("bpm", value.heartRate.toDouble()).put("min", value.min.toDouble()).put("max", value.max.toDouble()))
            }
            SamsungRecord("heart_rate:${point.uid}", "heart_rate", point.startTime, point.endTime ?: point.startTime, JSONObject()
                .put("bpm", point.getValue(DataType.HeartRateType.HEART_RATE)?.toDouble() ?: JSONObject.NULL)
                .put("min", point.getValue(DataType.HeartRateType.MIN_HEART_RATE)?.toDouble() ?: JSONObject.NULL)
                .put("max", point.getValue(DataType.HeartRateType.MAX_HEART_RATE)?.toDouble() ?: JSONObject.NULL)
                .put("series", series).put("source", source(point)))
        }

    private fun exercise(store: HealthDataStore, from: Instant, to: Instant): List<SamsungRecord> =
        read(store) { DataTypes.EXERCISE.readDataRequestBuilder.setInstantTimeFilter(InstantTimeFilter.of(from, to)) }.flatMap { point ->
            point.getValue(DataType.ExerciseType.SESSIONS).orEmpty().mapIndexed { index, session ->
                val route = JSONArray()
                SamsungHealthRecords.thin(session.route.orEmpty()).forEach { location ->
                    route.put(JSONObject().put("at", location.timestamp?.toString() ?: JSONObject.NULL)
                        .put("lat", location.latitude.toDouble()).put("lon", location.longitude.toDouble())
                        .put("accuracy", location.accuracy?.toDouble() ?: JSONObject.NULL))
                }
                SamsungRecord("exercise:${point.uid}:$index", "exercise", session.startTime, session.endTime, JSONObject()
                    .put("exercise_type", session.exerciseType?.name ?: JSONObject.NULL)
                    .put("title", session.customTitle ?: JSONObject.NULL)
                    .put("duration_seconds", session.duration?.seconds ?: JSONObject.NULL)
                    .put("distance_meters", session.distance?.toDouble() ?: JSONObject.NULL)
                    .put("calories", session.calories.toDouble())
                    .put("steps", session.count ?: JSONObject.NULL)
                    .put("mean_heart_rate", session.meanHeartRate?.toDouble() ?: JSONObject.NULL)
                    .put("max_heart_rate", session.maxHeartRate?.toDouble() ?: JSONObject.NULL)
                    .put("mean_speed", session.meanSpeed?.toDouble() ?: JSONObject.NULL)
                    .put("route_points", session.route?.size ?: 0)
                    .put("route", route)
                    .put("source", source(point)))
            }
        }

    private fun sleep(store: HealthDataStore, from: Instant, to: Instant): List<SamsungRecord> =
        read(store) { DataTypes.SLEEP.readDataRequestBuilder.setInstantTimeFilter(InstantTimeFilter.of(from, to)) }.map { point ->
            val sessions = JSONArray()
            point.getValue(DataType.SleepType.SESSIONS)?.forEach { session ->
                val stages = JSONArray()
                session.stages?.forEach { stage ->
                    stages.put(JSONObject().put("stage", stage.stage?.name ?: JSONObject.NULL)
                        .put("start", stage.startTime.toString()).put("end", stage.endTime.toString()))
                }
                sessions.put(JSONObject().put("start", session.startTime.toString()).put("end", session.endTime.toString()).put("stages", stages))
            }
            SamsungRecord("sleep:${point.uid}", "sleep", point.startTime, point.endTime ?: point.startTime, JSONObject()
                .put("score", point.getValue(DataType.SleepType.SLEEP_SCORE) ?: JSONObject.NULL)
                .put("duration_minutes", point.getValue(DataType.SleepType.DURATION)?.toMinutes() ?: JSONObject.NULL)
                .put("sessions", sessions).put("source", source(point)))
        }

    /** Steps per minute in the phone's time zone, grouped into hours. */
    private fun steps(store: HealthDataStore, from: Instant, to: Instant): List<SamsungRecord> {
        val zone = ZoneId.systemDefault()
        val minutes = mutableListOf<Pair<Instant, Long>>()
        var token: String? = null
        // A first read of 40 days is about 58 pages of minutes.
        for (page in 0 until MAX_PAGES * 3) {
            val request = DataType.StepsType.TOTAL.requestBuilder
                .setLocalTimeFilterWithGroup(LocalTimeFilter.of(LocalDateTime.ofInstant(from, zone), LocalDateTime.ofInstant(to, zone)),
                    LocalTimeGroup.of(LocalTimeGroupUnit.MINUTELY, 1))
                .setOrdering(Ordering.ASC).setPageSize(PAGE_SIZE)
            token?.let { request.setPageToken(it) }
            val response = store.aggregateDataAsync(request.build()).get(TIMEOUT_SECONDS, TimeUnit.SECONDS)
            response.dataList.forEach { item -> item.value?.let { minutes += item.startTime to it } }
            token = response.pageToken
            if (token.isNullOrBlank()) break
        }
        return SamsungHealthRecords.hourlySteps(minutes)
    }
}
