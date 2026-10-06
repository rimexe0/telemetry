package cyou.rime.telemetry.collector

import android.Manifest
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.provider.CalendarContract
import androidx.core.content.ContextCompat
import java.time.Duration
import java.time.Instant
import org.json.JSONObject

/**
 * Reads the owner's calendars from the phone (#26), read-only: the Google
 * calendars the phone already syncs, so no Google sign-in is needed. Each
 * occurrence from a day back to two weeks ahead becomes an observation; a
 * changed one is sent again as a revision, and one that disappears is sent
 * once as deleted. Only what is new or changed is sent. Descriptions and
 * attendees are not read. Without the calendar permission it does nothing.
 */
class CalendarCollector(private val context: Context, private val queue: HealthQueue, private val deviceId: String) {
    companion object {
        private const val PREFS = "telemetry-calendar"
        private const val KEY_SENT = "sent"
        val BACK: Duration = Duration.ofDays(1)
        val AHEAD: Duration = Duration.ofDays(14)
        const val EVERY_MILLIS = 15 * 60_000L

        fun permitted(context: Context): Boolean =
            ContextCompat.checkSelfPermission(context, Manifest.permission.READ_CALENDAR) == PackageManager.PERMISSION_GRANTED
    }

    data class Occurrence(val key: String, val begin: Instant, val end: Instant, val payload: JSONObject)

    /** Returns "ok", "permission_required" or an error state. */
    fun collect(now: Instant = Instant.now()): String {
        if (!permitted(context)) return "permission_required"
        val occurrences = runCatching { read(now.minus(BACK), now.plus(AHEAD)) }.getOrElse { return "error:${it.javaClass.simpleName}" }
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val sent = JSONObject(prefs.getString(KEY_SENT, "{}") ?: "{}")
        val records = mutableListOf<LocalObservation>()
        val current = JSONObject()
        val collectedAt = now.toString()
        fun observation(key: String, begin: Instant, end: Instant, payload: JSONObject) = LocalObservation(
            sourceInstanceKey = "android:$deviceId:calendar",
            sourceKind = "android_calendar",
            deviceId = deviceId,
            sourceRecordKey = key,
            kind = "android.calendar_event",
            recordType = "calendar_event",
            occurredAt = begin.toString(),
            endedAt = end.toString(),
            collectedAt = collectedAt,
            sourceSchemaVersion = "android-calendar-instances-1",
            payloadJson = payload.toString(),
        )
        for (occurrence in occurrences) {
            val digest = occurrence.payload.toString().hashCode().toString()
            current.put(occurrence.key, JSONObject().put("digest", digest).put("begin", occurrence.begin.toString()).put("end", occurrence.end.toString()))
            if (sent.optJSONObject(occurrence.key)?.optString("digest") != digest) {
                records += observation(occurrence.key, occurrence.begin, occurrence.end, occurrence.payload)
            }
        }
        // Gone from the calendar while still inside the window: deleted.
        val windowStart = now.minus(BACK)
        sent.keys().forEach { key ->
            if (current.has(key)) return@forEach
            val earlier = sent.optJSONObject(key) ?: return@forEach
            val begin = runCatching { Instant.parse(earlier.getString("begin")) }.getOrNull() ?: return@forEach
            val end = runCatching { Instant.parse(earlier.getString("end")) }.getOrNull() ?: begin
            if (!end.isBefore(windowStart)) {
                records += observation(key, begin, end, JSONObject().put("status", "deleted"))
            }
        }
        if (records.isNotEmpty()) queue.enqueue(records)
        prefs.edit().putString(KEY_SENT, current.toString()).apply()
        return "ok"
    }

    private fun read(from: Instant, to: Instant): List<Occurrence> {
        // Only the calendars the owner shows and syncs.
        val calendars = mutableMapOf<Long, String>()
        context.contentResolver.query(
            CalendarContract.Calendars.CONTENT_URI,
            arrayOf(CalendarContract.Calendars._ID, CalendarContract.Calendars.CALENDAR_DISPLAY_NAME),
            "${CalendarContract.Calendars.VISIBLE} = 1 AND ${CalendarContract.Calendars.SYNC_EVENTS} = 1",
            null, null,
        )?.use { cursor ->
            while (cursor.moveToNext()) calendars[cursor.getLong(0)] = cursor.getString(1).orEmpty()
        }
        val uri = CalendarContract.Instances.CONTENT_URI.buildUpon().also {
            ContentUris.appendId(it, from.toEpochMilli())
            ContentUris.appendId(it, to.toEpochMilli())
        }.build()
        val occurrences = mutableListOf<Occurrence>()
        context.contentResolver.query(
            uri,
            arrayOf(
                CalendarContract.Instances.EVENT_ID, CalendarContract.Instances.BEGIN, CalendarContract.Instances.END,
                CalendarContract.Instances.TITLE, CalendarContract.Instances.EVENT_LOCATION, CalendarContract.Instances.ALL_DAY,
                CalendarContract.Instances.CALENDAR_ID, CalendarContract.Instances.STATUS, CalendarContract.Instances.AVAILABILITY,
            ),
            null, null, null,
        )?.use { cursor ->
            while (cursor.moveToNext()) {
                val calendar = calendars[cursor.getLong(6)] ?: continue
                val begin = Instant.ofEpochMilli(cursor.getLong(1))
                val end = Instant.ofEpochMilli(cursor.getLong(2))
                val status = when (cursor.getInt(7)) {
                    CalendarContract.Events.STATUS_CANCELED -> "cancelled"
                    CalendarContract.Events.STATUS_TENTATIVE -> "tentative"
                    else -> "confirmed"
                }
                val payload = JSONObject()
                    .put("event_id", cursor.getLong(0))
                    .put("title", cursor.getString(3).orEmpty())
                    .put("location", cursor.getString(4).orEmpty())
                    .put("all_day", cursor.getInt(5) == 1)
                    .put("calendar", calendar)
                    .put("status", status)
                    .put("busy", cursor.getInt(8) != CalendarContract.Events.AVAILABILITY_FREE)
                    .put("start", begin.toString())
                    .put("end", end.toString())
                occurrences += Occurrence("event:${cursor.getLong(0)}:${begin.toEpochMilli()}", begin, end, payload)
            }
        }
        return occurrences
    }
}
