package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.PowerManager
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

data class TaskRef(val id: Long, val title: String)

/** A task whose agent stopped at [since] and waits for the owner. */
data class TurnRef(val taskId: Long, val task: String, val since: Instant)

/** The running focus session and how what is on the Mac stands against its task. */
data class FocusView(
    val taskId: Long,
    val task: String,
    val nextAction: String?,
    /** "on", "off", "unknown" or "away". */
    val on: String,
    val context: String?,
    val offSince: Instant?,
)

/** What the server reads at the Mac right now, as the phone needs it. */
data class TrackingView(
    /** "away", "distraction", "task" or "other". */
    val state: String,
    val tasks: List<TaskRef>,
    val yourTurn: List<TurnRef>,
    val focus: FocusView?,
)

object TrackingModel {
    fun parse(json: String): TrackingView {
        val root = JSONObject(json)
        val tracking = root.optJSONObject("tracking") ?: JSONObject()
        val tasks = root.optJSONArray("tasks")
        val turns = tracking.optJSONArray("your_turn")
        val focus = tracking.optJSONObject("focus")
        return TrackingView(
            state = tracking.optString("state", "away"),
            tasks = if (tasks == null) emptyList() else (0 until tasks.length()).map { tasks.getJSONObject(it).let { task -> TaskRef(task.getLong("id"), task.getString("title")) } },
            yourTurn = if (turns == null) emptyList() else (0 until turns.length()).map {
                turns.getJSONObject(it).let { turn -> TurnRef(turn.getLong("task_id"), turn.getString("task"), Instant.parse(turn.getString("since"))) }
            },
            focus = focus?.let {
                FocusView(it.getLong("task_id"), it.getString("task"), it.optString("next_action").ifBlank { null }, it.optString("on"),
                    it.optString("context").ifBlank { null }, it.optString("off_since").ifBlank { null }?.let(Instant::parse))
            },
        )
    }

    /** One line on how the focus session stands, for the home screen. */
    fun focusLine(focus: FocusView, now: Instant): String = when (focus.on) {
        "on" -> "On it"
        "away" -> "Away from the Mac"
        else -> {
            val minutes = focus.offSince?.let { java.time.Duration.between(it, now).toMinutes() } ?: 0
            listOfNotNull("Away from it for $minutes min", focus.context).joinToString(" · ")
        }
    }
}

/**
 * Tells the owner on the phone what they would otherwise have to remember:
 * that a task's agent has stopped while they are away from the Mac, and, at
 * the first use of the day, what today's tasks are. Each is said once.
 */
object TurnWatch {
    private const val PREFS = "telemetry-turns"
    private const val KEY_TOLD = "told"
    private const val KEY_DAY = "day"
    private const val CHANNEL = "telemetry-turns"
    private const val BASE_ID = 4800
    private const val DAY_ID = 4799

    /** The stops not announced yet, when the owner is not at the Mac to see them there. */
    fun due(view: TrackingView, told: Set<String>): List<TurnRef> =
        if (view.state != "away") emptyList() else view.yourTurn.filter { key(it) !in told }

    fun key(turn: TurnRef): String = "${turn.taskId}:${turn.since}"

    fun check(context: Context, json: String?) {
        val view = json?.let { runCatching { TrackingModel.parse(it) }.getOrNull() } ?: return
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val told = preferences.getStringSet(KEY_TOLD, emptySet()).orEmpty()
        val manager = context.getSystemService(NotificationManager::class.java)
        val editor = preferences.edit()
        val open = view.yourTurn.map { key(it) }.toSet()
        for (turn in due(view, told)) {
            val time = DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault()).format(turn.since)
            notify(context, manager, BASE_ID + (turn.taskId % 100).toInt(), "Your turn: ${turn.task}", "Its agent stopped at $time.")
        }
        // Once the owner is back on a task its notice goes away by itself.
        for (old in told - open) manager.cancel(BASE_ID + (old.substringBefore(':').toLongOrNull() ?: 0).rem(100).toInt())
        editor.putStringSet(KEY_TOLD, (told intersect open) + due(view, told).map { key(it) })

        // The day's tasks, once, the first time the phone is in use that day.
        val today = Instant.now().atZone(ZoneId.systemDefault()).toLocalDate().toString()
        val interactive = context.getSystemService(PowerManager::class.java).isInteractive
        if (interactive && view.tasks.isNotEmpty() && preferences.getString(KEY_DAY, null) != today) {
            editor.putString(KEY_DAY, today)
            notify(context, manager, DAY_ID, "Today", view.tasks.joinToString(" · ") { it.title })
        }
        editor.apply()
    }

    private fun notify(context: Context, manager: NotificationManager, id: Int, title: String, text: String) {
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Tasks", NotificationManager.IMPORTANCE_DEFAULT))
        val open = PendingIntent.getActivity(context, 5, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        manager.notify(id, Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(Notification.BigTextStyle().bigText(text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build())
    }
}
