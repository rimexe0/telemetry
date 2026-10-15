package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

data class CollectionIncident(
    val id: Long,
    val device: String,
    val kind: String,
    val since: Instant,
    val message: String,
    val deviceName: String = device,
)

/**
 * Tells the owner when another device's collection has a problem (#28), once
 * per incident, and clears the notice when the server closes it. The phone's
 * own silence cannot be reported from the phone. A collector stopped while
 * its computer is on is the way around blocking, so that one alerts loudly,
 * stays, and alerts again every 15 minutes until it runs.
 */
object IncidentWatch {
    private const val PREFS = "telemetry-incident-watch"
    private const val KEY_SHOWN = "shown_v2"
    private const val CHANNEL = "telemetry-incidents"
    private const val BASE_ID = 4300
    private const val STOPPED_CHANNEL = "telemetry-stopped"
    private const val KEY_ALERTED = "alerted:"
    const val STOPPED = "stopped_while_on"
    const val STOPPED_REPEAT_MILLIS = 15 * 60_000L

    /** Whether a stopped collector should alert again now. */
    fun alertsAgain(lastAlert: Long?, now: Long): Boolean = lastAlert == null || now - lastAlert >= STOPPED_REPEAT_MILLIS

    fun parse(json: String): List<CollectionIncident> {
        val array = JSONObject(json).optJSONArray("collection_incidents") ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            val item = array.getJSONObject(index)
            runCatching {
                CollectionIncident(
                    id = item.getLong("id"),
                    device = item.getString("device"),
                    kind = item.getString("kind"),
                    since = Instant.parse(item.getString("started_at")),
                    message = item.optJSONObject("detail")?.optString("message").orEmpty(),
                    deviceName = item.optJSONObject("detail")?.optString("device_name").orEmpty()
                        .removeSuffix(".local").ifBlank { item.getString("device") },
                )
            }.getOrNull()
        }.filter { it.device != "phone" }
    }

    fun check(context: Context, json: String?) {
        val incidents = json?.let { runCatching { parse(it) }.getOrNull() } ?: return
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        // A new key re-posts incidents an earlier build showed silently on the
        // collection service's low-importance channel.
        val shown = preferences.getStringSet(KEY_SHOWN, emptySet()).orEmpty().mapNotNull { it.toLongOrNull() }.toSet()
        val open = incidents.map { it.id }.toSet()
        val manager = context.getSystemService(NotificationManager::class.java)
        val editor = preferences.edit()
        val now = System.currentTimeMillis()
        for (incident in incidents) {
            if (incident.kind == STOPPED) {
                val last = preferences.getLong(KEY_ALERTED + incident.id, 0L).takeIf { it > 0 }
                if (alertsAgain(last, now)) {
                    notify(context, manager, incident)
                    editor.putLong(KEY_ALERTED + incident.id, now)
                }
            } else if (incident.id !in shown) notify(context, manager, incident)
        }
        for (id in shown - open) {
            manager.cancel(notificationId(id))
            editor.remove(KEY_ALERTED + id)
        }
        editor.putStringSet(KEY_SHOWN, open.map { it.toString() }.toSet()).apply()
    }

    fun title(incident: CollectionIncident): String {
        val device = incident.deviceName
        val time = DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault()).format(incident.since)
        val kind = incident.kind
        return when {
            kind == STOPPED -> "Telemetry is off on $device since $time"
            kind == "silent" -> "$device: no data since $time"
            kind == "titles_unreadable" -> "$device can't read window titles"
            kind == "window_sampling_stopped" -> "$device stopped recording windows at $time"
            kind.startsWith("module_failing:") -> "$device: ${kind.removePrefix("module_failing:")} collection failing since $time"
            else -> "$device collection problem since $time"
        }
    }

    private fun notificationId(id: Long) = BASE_ID + (id % 1000).toInt()

    private fun notify(context: Context, manager: NotificationManager, incident: CollectionIncident) {
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Collection problems", NotificationManager.IMPORTANCE_DEFAULT))
        manager.createNotificationChannel(NotificationChannel(STOPPED_CHANNEL, "Telemetry stopped on a computer", NotificationManager.IMPORTANCE_HIGH))
        val open = PendingIntent.getActivity(context, 3, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stopped = incident.kind == STOPPED
        manager.notify(notificationId(incident.id), Notification.Builder(context, if (stopped) STOPPED_CHANNEL else CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_error)
            .setOngoing(stopped)
            .setContentTitle(title(incident))
            .setContentText(incident.message)
            .setStyle(Notification.BigTextStyle().bigText(incident.message))
            .setContentIntent(open)
            .build())
    }
}
