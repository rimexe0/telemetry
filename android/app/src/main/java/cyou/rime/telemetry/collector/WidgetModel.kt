package cyou.rime.telemetry.collector

import java.time.Duration
import java.time.Instant
import java.time.LocalTime
import java.time.ZoneId
import java.time.format.TextStyle
import java.util.Locale

/** One habit row of the home-screen widget. */
data class WidgetRow(
    val id: Long,
    val name: String,
    val done: Boolean,
    /** "+30", or "" when unknown. */
    val worth: String,
    /** The last seven days, oldest first, drawn behind the name. */
    val week: List<CellState> = emptyList(),
    /** Under a habit with a rule: "Karantina İskelesi · 420 m away". */
    val rule: String? = null,
)

/** What the home-screen widget shows, from what the phone has saved. */
data class WidgetState(
    /** "Habits 2 of 5 · 1 h 33 min left". */
    val header: String,
    /** Null when nothing saved says what today's habits are. */
    val rows: List<WidgetRow>?,
    /** Today's words when there are no rows to tick, as Today says them. */
    val empty: String?,
    val ticksOpen: Boolean,
    /** "saved 14:20" once the saved copy is old, else null. */
    val saved: String?,
    /** Nothing was saved on the phone yet: grey rows where the habits go, with no words. */
    val placeholder: Boolean = false,
)

/**
 * The widget's words and rows, worked out as Today works them out
 * ([HomeState.today]): the same day, habits, queued ticks and budget, so
 * the widget and the app never disagree.
 */
object WidgetModel {
    /** The collection cycle refreshes the saved copy every few minutes; older than this it says when it is from. */
    val STALE_AFTER: Duration = Duration.ofMinutes(15)

    fun state(saved: Saved, savedAt: Instant?, now: Instant, zone: ZoneId, latest: RuleFix? = null): WidgetState {
        val today = HomeState.today(saved, now, zone)
        val lines = ruleLines(today.habits, latest, now, zone)
        val habits = today.habits
        val budget = today.budget
        val worthShown = budget?.enabled != false
        return WidgetState(
            header = header(habits, budget),
            // A ticked habit leaves the list; it is unticked in the app
            // (owner 2026-11-26).
            rows = habits?.filter { !it.done }?.map {
                WidgetRow(it.id, it.name, it.done, if (worthShown) HabitCells.worth(it.value) else "", today.strips[it.id].orEmpty().map { cell -> cell.state }, lines[it.id])
            },
            empty = when {
                habits == null && savedAt == null -> null
                habits == null -> "Unknown: today's habits were never saved on this phone."
                habits.isEmpty() -> "No habits scheduled today."
                !today.ticksOpen -> "Bedtime has started, so today's habits are closed."
                habits.all { it.done } -> "All of today's habits are done."
                else -> null
            },
            ticksOpen = today.ticksOpen,
            saved = savedLine(savedAt, now, zone),
            placeholder = habits == null && savedAt == null,
        )
    }

    /** "Habits 2 of 5 · 1 h 33 min left": the counts while habits are known, the time left while today's budget is. */
    fun header(habits: List<TodayHabit>?, budget: BudgetView?): String {
        val parts = mutableListOf(if (habits.isNullOrEmpty()) "Habits" else "Habits ${habits.count { it.done }} of ${habits.size}")
        if (budget != null && budget.enabled) parts += "${BudgetText.minutes(budget.left)} left"
        return parts.joinToString(" · ")
    }

    /** "saved 14:20", with the date when it was another day; null while the copy is fresh. */
    fun savedLine(at: Instant?, now: Instant, zone: ZoneId): String? {
        if (at == null || Duration.between(at, now) < STALE_AFTER) return null
        val date = at.atZone(zone).toLocalDate()
        val day = if (date == now.atZone(zone).toLocalDate()) "" else "${date.dayOfMonth} ${date.month.getDisplayName(TextStyle.SHORT, Locale.UK)} "
        return "saved $day${BudgetText.clock(at, zone)}"
    }

    /**
     * What a tap on [habitId] records: the day and the new state, against
     * what the habit is now (queued ticks included), so two quick taps tick
     * and untick. Null when it cannot be ticked: not today's, or bedtime has
     * started.
     */
    fun tap(saved: Saved, habitId: Long, now: Instant, zone: ZoneId): Pair<String, Boolean>? {
        val today = HomeState.today(saved, now, zone)
        if (!today.ticksOpen) return null
        val habit = today.habits?.firstOrNull { it.id == habitId } ?: return null
        return today.day to !habit.done
    }

    /**
     * When the widget next has to be drawn again without new data: the next
     * day start, or bedtime closing today's ticks if that comes first.
     */
    fun nextChange(saved: Saved, now: Instant, zone: ZoneId): Instant {
        val bedtimes = HabitDay.bedtimes(saved.nowJson)
        val clock = HabitDay.dayStartClock(bedtimes, HabitDay.timelineEnd(saved.nowJson), zone)
        val day = HabitDay.today(now, zone, clock, saved.budget?.day)
        val nextStart = nextClock(now, zone, clock)
        val close = HabitDay.ticksCloseAt(day, zone, clock, saved.budget, bedtimes)?.takeIf { it.isAfter(now) }
        return if (close != null && close.isBefore(nextStart)) close else nextStart
    }

    private fun nextClock(now: Instant, zone: ZoneId, clock: LocalTime): Instant {
        val local = now.atZone(zone)
        val todayAt = local.toLocalDate().atTime(clock).atZone(zone).toInstant()
        return if (todayAt.isAfter(now)) todayAt else local.toLocalDate().plusDays(1).atTime(clock).atZone(zone).toInstant()
    }
}
