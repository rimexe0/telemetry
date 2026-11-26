package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import org.json.JSONObject
import java.time.Duration
import java.time.Instant

/**
 * Something the owner must come back to at a time, or on arriving at or
 * leaving a place; the server holds it. A place follow-up comes due when the
 * place is reached (triggeredAt), and no earlier than dueAt.
 */
data class FollowUp(val id: Long, val title: String, val dueAt: Instant, val nagMinutes: Int?,
                    val place: ReminderPlace? = null, val triggeredAt: Instant? = null) {
    /** When it came due or comes due; null while it waits for its place. */
    val dueSince: Instant? get() = when {
        place == null -> dueAt
        triggeredAt == null -> null
        triggeredAt.isAfter(dueAt) -> triggeredAt
        else -> dueAt
    }

    fun dueBy(now: Instant): Boolean = dueSince?.let { !now.isBefore(it) } == true
}

/**
 * Keeps due follow-ups in front of the owner until they are marked done, so
 * nothing depends on remembering, and says so in a notification when one
 * comes due (and again at its nag interval). A swiped-away notification
 * stays away, and with follow-up notifications switched off there are none
 * (owner request 2026-11-22). It works from the cached server answer, so
 * a follow-up still comes due while the phone is offline; a "done" tapped
 * offline is kept and sent when the server is reachable.
 */
object FollowUpWatch {
    private const val PREFS = "telemetry-follow-ups"
    private const val KEY_DONE = "done"
    private const val KEY_ALERTED = "alerted:"
    private const val CHANNEL = "telemetry-follow-ups"
    private const val QUIET_CHANNEL = "telemetry-follow-ups-quiet"
    private const val BASE_ID = 4600
    const val ACTION_DONE = "cyou.rime.telemetry.collector.FOLLOW_UP_DONE"
    const val EXTRA_ID = "id"

    fun parse(json: String): List<FollowUp> {
        val array = JSONObject(json).optJSONArray("follow_ups") ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            val item = array.getJSONObject(index)
            runCatching {
                FollowUp(item.getLong("id"), item.getString("title"), Instant.parse(item.getString("due_at")),
                    if (item.isNull("nag_minutes")) null else item.optInt("nag_minutes").takeIf { it > 0 },
                    ReminderPlace.parse(item.optJSONObject("place")),
                    item.optString("triggered_at").takeIf { it.isNotBlank() && it != "null" }?.let { runCatching { Instant.parse(it) }.getOrNull() })
            }.getOrNull()
        }
    }

    /** Whether a due follow-up should make a sound now: the first time, and again every nag interval. */
    fun alertsNow(followUp: FollowUp, now: Instant, lastAlert: Instant?): Boolean = when {
        !followUp.dueBy(now) -> false
        lastAlert == null -> true
        followUp.nagMinutes == null -> false
        else -> Duration.between(lastAlert, now) >= Duration.ofMinutes(followUp.nagMinutes.toLong())
    }

    /** A place this phone saw reached counts before the server hears of it. */
    private fun withLocalTrigger(context: Context, followUp: FollowUp): FollowUp =
        if (followUp.place != null && followUp.triggeredAt == null) followUp.copy(triggeredAt = PlaceReminders.firedAt(context, followUp.id)) else followUp

    fun check(context: Context, json: String?) {
        flush(context)
        val listed = json?.let { runCatching { parse(it) }.getOrNull() }?.map { withLocalTrigger(context, it) } ?: return
        val preferences = prefs(context)
        val done = doneLocally(context)
        val now = Instant.now()
        val manager = context.getSystemService(NotificationManager::class.java)
        val wanted = Notices.on(context, NoticeKind.FOLLOW_UPS)
        val editor = preferences.edit()
        for (followUp in listed) {
            if (followUp.id in done || !followUp.dueBy(now)) continue
            if (!wanted) manager.cancel(notificationId(followUp.id))
            val last = preferences.getString(KEY_ALERTED + followUp.id, null)?.let { runCatching { Instant.parse(it) }.getOrNull() }
            if (!alertsNow(followUp, now, last)) continue
            if (wanted) notify(context, manager, followUp)
            editor.putString(KEY_ALERTED + followUp.id, now.toString())
        }
        // Closed elsewhere: take the notice down and forget it.
        val openIds = listed.map { it.id }.toSet()
        for (key in preferences.all.keys.filter { it.startsWith(KEY_ALERTED) }) {
            val id = key.removePrefix(KEY_ALERTED).toLongOrNull() ?: continue
            if (id !in openIds) {
                manager.cancel(notificationId(id))
                editor.remove(key)
            }
        }
        editor.apply()
    }

    /** Marks it done here at once and tells the server, now or when it is reachable. Blocking IO. */
    fun markDone(context: Context, id: Long) {
        val preferences = prefs(context)
        preferences.edit().putStringSet(KEY_DONE, doneLocally(context).map { it.toString() }.toSet() + id.toString()).apply()
        context.getSystemService(NotificationManager::class.java).cancel(notificationId(id))
        flush(context)
    }

    private fun flush(context: Context) {
        val pending = doneLocally(context)
        if (pending.isEmpty()) return
        val sent = pending.filter { DeviceApi.post(context, "/v1/device/follow-ups/$it/done") }.toSet()
        if (sent.isNotEmpty()) prefs(context).edit().putStringSet(KEY_DONE, (doneLocally(context) - sent).map { it.toString() }.toSet()).apply()
    }

    private fun doneLocally(context: Context): Set<Long> =
        prefs(context).getStringSet(KEY_DONE, emptySet()).orEmpty().mapNotNull { it.toLongOrNull() }.toSet()

    private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    private fun notificationId(id: Long) = BASE_ID + (id % 1000).toInt()

    private fun notify(context: Context, manager: NotificationManager, followUp: FollowUp) {
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Follow-ups", NotificationManager.IMPORTANCE_HIGH))
        // Earlier builds re-posted swiped-away ones quietly on a channel of their own.
        manager.deleteNotificationChannel(QUIET_CHANNEL)
        val open = PendingIntent.getActivity(context, 4, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val done = PendingIntent.getBroadcast(context, notificationId(followUp.id),
            Intent(context, FollowUpDoneReceiver::class.java).setAction(ACTION_DONE).putExtra(EXTRA_ID, followUp.id),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        manager.notify(notificationId(followUp.id), Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_popup_reminder)
            .setContentTitle(followUp.title)
            .setContentText(dueText(followUp, Instant.now()))
            .setWhen((followUp.dueSince ?: followUp.dueAt).toEpochMilli())
            .setShowWhen(true)
            .setContentIntent(open)
            .addAction(Notification.Action.Builder(null, "Done", done).build())
            .build())
    }
}

/** "Due since 5 min", or for a place "Arrived home 5 min ago". */
internal fun dueText(followUp: FollowUp, now: Instant): String {
    val since = followUp.dueSince ?: followUp.dueAt
    val place = followUp.place ?: return "Due since ${NowModel.ageText(since, now).removeSuffix(" ago")}"
    val name = if (place.home) "home" else place.name
    return "${if (place.arrive) "Arrived at" else "Left"} $name ${NowModel.ageText(since, now)}"
}

/** The "Done" button on a follow-up notification. */
class FollowUpDoneReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val id = intent.getLongExtra(FollowUpWatch.EXTRA_ID, 0)
        if (intent.action != FollowUpWatch.ACTION_DONE || id <= 0) return
        val pending = goAsync()
        Thread {
            runCatching { FollowUpWatch.markDone(context.applicationContext, id) }
            pending.finish()
        }.start()
    }
}
