package cyou.rime.telemetry.collector

import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import org.json.JSONArray
import org.json.JSONObject

/** One thing read from Samsung Health, in Telemetry's shape. */
data class SamsungRecord(val key: String, val type: String, val start: Instant, val end: Instant, val payload: JSONObject)

/**
 * Samsung Health read directly (heart rate, workouts with their routes, sleep
 * with stages, steps per minute), because on this phone Samsung Health does
 * not write to Health Connect. This part has no Samsung code: it turns
 * records into observations and sends only what is new or changed, so a
 * workout or night that Samsung Health fills in later is sent again as a
 * revision.
 */
object SamsungHealthRecords {
    const val PREFS = "telemetry-samsung-health"
    const val KEY_SENT = "sent"
    const val KEY_BACKFILLED = "backfilled"
    const val KEY_GRANTED = "granted"
    const val EVERY_MILLIS = 5 * 60_000L
    const val SOURCE_KIND = "samsung_health"
    const val SCHEMA = "samsung-health-data-sdk-1.1.0"

    /** The first read reaches back over the weeks the watch data was lost. */
    val BACKFILL: Duration = Duration.ofDays(40)

    /** Later reads look this far back, for data the watch syncs late. */
    val WINDOW: Duration = Duration.ofDays(2)

    /** Routes are thinned to at most this many points per workout. */
    const val MAX_ROUTE_POINTS = 600

    /** Keeps the records that are new or differ from what was sent, and what to remember as sent. */
    fun changed(records: List<SamsungRecord>, sent: JSONObject, now: Instant): Pair<List<SamsungRecord>, JSONObject> {
        val remembered = JSONObject()
        val keepFrom = now.minus(WINDOW).minus(Duration.ofDays(1))
        // Earlier sends still inside the window stay remembered.
        sent.keys().forEach { key ->
            val entry = sent.optJSONObject(key) ?: return@forEach
            val end = runCatching { Instant.parse(entry.getString("end")) }.getOrNull() ?: return@forEach
            if (!end.isBefore(keepFrom)) remembered.put(key, entry)
        }
        val fresh = mutableListOf<SamsungRecord>()
        for (record in records) {
            val digest = record.payload.toString().hashCode().toString()
            if (sent.optJSONObject(record.key)?.optString("digest") != digest) fresh += record
            if (!record.end.isBefore(keepFrom)) remembered.put(record.key, JSONObject().put("digest", digest).put("end", record.end.toString()))
        }
        return fresh to remembered
    }

    fun observations(deviceId: String, records: List<SamsungRecord>, collectedAt: Instant): List<LocalObservation> = records.map { record ->
        LocalObservation(
            sourceInstanceKey = "android:$deviceId:samsung_health",
            sourceKind = SOURCE_KIND,
            deviceId = deviceId,
            sourceRecordKey = record.key,
            kind = "samsung_health.${record.type}",
            recordType = record.type,
            occurredAt = record.start.toString(),
            endedAt = record.end.toString(),
            collectedAt = collectedAt.toString(),
            sourceSchemaVersion = SCHEMA,
            payloadJson = record.payload.toString(),
        )
    }

    /** Steps per minute become one record per hour, so a day is 24 records, not 1,440. */
    fun hourlySteps(minutes: List<Pair<Instant, Long>>): List<SamsungRecord> =
        minutes.filter { it.second > 0 }.groupBy { it.first.truncatedTo(ChronoUnit.HOURS) }.toSortedMap().map { (hour, counts) ->
            val array = JSONArray()
            counts.sortedBy { it.first }.forEach { (at, count) -> array.put(JSONObject().put("start", at.toString()).put("count", count)) }
            SamsungRecord("steps:${hour.toEpochMilli()}", "steps", hour, hour.plus(Duration.ofHours(1)),
                JSONObject().put("total", counts.sumOf { it.second }).put("minutes", array))
        }

    /** Keeps every n-th point so a long workout's route stays a sensible size; the last point is kept. */
    fun <T> thin(points: List<T>, max: Int = MAX_ROUTE_POINTS): List<T> {
        if (points.size <= max) return points
        val step = (points.size + max - 1) / max
        val kept = points.filterIndexed { index, _ -> index % step == 0 }
        return if (kept.last() == points.last()) kept else kept + points.last()
    }
}
