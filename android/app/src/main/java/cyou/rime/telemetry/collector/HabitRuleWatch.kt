package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.os.PowerManager
import org.json.JSONArray
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.util.concurrent.Executors

/**
 * Works out the habit rules on the phone itself (owner request 2026-11-26),
 * so a place habit ticks the moment the owner has been there long enough,
 * with the server down too. Positions come from the location service the
 * app already runs; this keeps the last few hours of them. A rule's tick
 * goes into the same queue as a tap ([HabitTicks]), marked as the rule's,
 * with the moment the rule was met.
 *
 * It also says the morning notice: once a day per place habit, when its
 * window is open, it is not done, and the screen is on.
 */
object HabitRuleWatch {
    private const val PREFS = "telemetry-habit-rules"
    private const val KEY_FIXES = "fixes"
    private const val KEY_FIRED = "fired:"
    private const val KEY_SAID = "said:"
    private const val CHANNEL = "telemetry-habits"
    private const val NOTIFICATION_BASE = 4300

    /** Positions kept: long enough for a morning window. */
    private val KEEP: Duration = Duration.ofHours(6)
    private const val MAX_FIXES = 720

    private val worker = Executors.newSingleThreadExecutor()

    /** A new position from the location service. Checks the rules off the main thread. */
    fun onPosition(context: Context, fix: RuleFix) {
        val app = context.applicationContext
        worker.execute {
            runCatching {
                save(app, fix)
                evaluate(app, notify = false)
            }
        }
    }

    /** Every collection cycle ([PolicyWatch]): the rules, and the morning notice. Blocking IO. */
    fun check(context: Context) {
        runCatching { evaluate(context.applicationContext, notify = true) }
    }

    /** The phone's latest position, or null. */
    fun latest(context: Context): RuleFix? = fixes(context).maxByOrNull { it.at }

    /**
     * Whether a place habit's window is open now and the habit not done: the
     * location service then asks for positions even while the phone stays
     * still, so a stay at the place is seen. Outside the window it does not.
     */
    fun wantsPrecise(context: Context, now: Instant = Instant.now()): Boolean = runCatching {
        val today = HomeState.today(Saved.read(context), now, ZoneId.systemDefault())
        today.ticksOpen && today.habits.orEmpty().any { habit ->
            habit.rule != null && !habit.done && !habit.unticked && today.windows[habit.id]?.open(now) == true
        }
    }.getOrDefault(false)

    @Synchronized
    private fun evaluate(context: Context, notify: Boolean) {
        val now = Instant.now()
        val zone = ZoneId.systemDefault()
        val today = HomeState.today(Saved.read(context), now, zone)
        val habits = today.habits.orEmpty().filter { it.rule != null }
        if (habits.isEmpty() || !today.ticksOpen) return
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val queued = HabitTicks.queue(context).entries().filter { it.day == today.day }.map { it.habitId }.toSet()
        val fixes = fixes(context)
        var ticked = false
        for (habit in habits) {
            val rule = habit.rule ?: continue
            val window = today.windows[habit.id] ?: continue
            val firedKey = "$KEY_FIRED${today.day}:${habit.id}"
            if (!HabitRuleEval.mayTick(habit.done, habit.unticked, habit.id in queued, preferences.contains(firedKey))) continue
            val reached = HabitRuleEval.reached(rule, fixes, window.from, minOf(window.until, now)) ?: continue
            preferences.edit().putString(firedKey, reached.toString()).commit()
            HabitTicks.recordRule(context, habit.id, today.day, reached)
            ticked = true
        }
        if (ticked) {
            runCatching { HabitTicks.flush(context) }
            cancelNotices(context, habits.map { it.id })
        }
        // The distance on the widget and Today follows the position.
        if (!notify && habits.any { !it.done }) HabitWidgets.changed(context)
        if (notify) notice(context, today, habits, fixes.maxByOrNull { it.at }, now)
        forgetOldKeys(preferences, today.day)
    }

    private fun notice(context: Context, today: TodayState, habits: List<TodayHabit>, latest: RuleFix?, now: Instant) {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        val interactive = context.getSystemService(PowerManager::class.java)?.isInteractive == true
        for (habit in habits) {
            val rule = habit.rule ?: continue
            val saidKey = "$KEY_SAID${habit.id}"
            val said = preferences.getString(saidKey, null)
            // Done since it was said: the notice goes away.
            if (habit.done && said == today.day) manager.cancel(NOTIFICATION_BASE + (habit.id % 1000).toInt())
            if (!Notices.on(context, NoticeKind.HABIT_PLACES)) continue
            if (!RuleNotice.due(today.windows[habit.id], now, habit.done, habit.unticked, interactive, today.day, said)) continue
            // Said once: marked before it is shown, so a crash never repeats it.
            preferences.edit().putString(saidKey, today.day).commit()
            manager.createNotificationChannel(NotificationChannel(CHANNEL, "Habits", NotificationManager.IMPORTANCE_DEFAULT))
            val text = RuleText.notice(habit.name, rule, latest, now)
            val open = PendingIntent.getActivity(context, 3, HabitWidgets.openToday(context), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
            manager.notify(NOTIFICATION_BASE + (habit.id % 1000).toInt(), Notification.Builder(context, CHANNEL)
                .setSmallIcon(android.R.drawable.ic_menu_mylocation)
                .setContentTitle(habit.name)
                .setContentText(text)
                .setStyle(Notification.BigTextStyle().bigText(text))
                .setContentIntent(open)
                .setAutoCancel(true)
                .build())
        }
    }

    private fun cancelNotices(context: Context, ids: List<Long>) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        for (id in ids) manager.cancel(NOTIFICATION_BASE + (id % 1000).toInt())
    }

    /** Fired marks are per day; older ones are dropped. */
    private fun forgetOldKeys(preferences: android.content.SharedPreferences, day: String) {
        val old = preferences.all.keys.filter { it.startsWith(KEY_FIRED) && !it.startsWith("$KEY_FIRED$day:") }
        if (old.isEmpty()) return
        preferences.edit().apply { old.forEach { remove(it) } }.apply()
    }

    @Synchronized
    private fun save(context: Context, fix: RuleFix) {
        val cutoff = fix.at.minus(KEEP)
        val kept = (fixes(context) + fix).filter { it.at.isAfter(cutoff) }.sortedBy { it.at }.takeLast(MAX_FIXES)
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_FIXES, encode(kept)).commit()
    }

    private fun fixes(context: Context): List<RuleFix> =
        decode(context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_FIXES, null))

    fun encode(fixes: List<RuleFix>): String = JSONArray().apply {
        for (fix in fixes) put(JSONArray().put(fix.at.toEpochMilli()).put(fix.lat).put(fix.lon).put(fix.accuracy))
    }.toString()

    fun decode(json: String?): List<RuleFix> {
        val array = json?.let { runCatching { JSONArray(it) }.getOrNull() } ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONArray(index) ?: return@mapNotNull null
            runCatching { RuleFix(Instant.ofEpochMilli(item.getLong(0)), item.getDouble(1), item.getDouble(2), item.getDouble(3)) }.getOrNull()
        }
    }
}
