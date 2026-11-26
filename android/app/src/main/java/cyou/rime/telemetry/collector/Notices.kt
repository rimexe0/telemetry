package cyou.rime.telemetry.collector

import android.content.Context

/**
 * The kinds of notification the app may send, each with its own switch in
 * Settings. The owner found most of them irritating (2026-11-22) and called
 * them useless (2026-11-24), so only the sync notice and follow-ups start
 * on, and a switched-off kind stays off: nothing in the app switches one
 * back on or asks for it again.
 */
enum class NoticeKind(val title: String, val detail: String, val defaultOn: Boolean) {
    SYNC("This phone has not synced", "Once, after records have waited half an hour, with Sync now. Also when the phone was removed in the panel.", true),
    FOLLOW_UPS("Reminders when they are due", "A swiped-away one stays away.", true),
    BLOCKING_OFF("Blocking switched off", "Once each time blocking is switched off while something is blocked.", false),
    COMPUTER_STOPPED("Telemetry stopped on a computer", "Once each time, not repeated.", false),
    COMPUTER_PROBLEMS("Other collection problems on a computer", "Such as a computer that went quiet or can't read window titles.", false),
    TASKS("Tasks", "When a coding agent waits for you, and the day's tasks in the morning.", false),
}

object Notices {
    private const val PREFS = "telemetry-notices"

    fun on(context: Context, kind: NoticeKind): Boolean =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(kind.name, kind.defaultOn)

    fun set(context: Context, kind: NoticeKind, on: Boolean) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(kind.name, on).apply()
    }
}
