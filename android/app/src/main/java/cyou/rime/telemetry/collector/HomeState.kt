package cyou.rime.telemetry.collector

import android.content.Context
import java.time.Instant
import java.time.ZoneId

/** What Today shows, worked out from the saved answers and the tick queue. */
data class TodayState(
    val day: String,
    /** Null when nothing saved says what today's habits are. */
    val habits: List<TodayHabit>?,
    val strips: Map<Long, List<WeekCell>>,
    /** False once today's bedtime has started. */
    val ticksOpen: Boolean,
    /** The budget, only when the saved one is for today. */
    val budget: BudgetView?,
    /** The saved budget is for an earlier day, so today's is unknown. */
    val budgetUnknown: Boolean,
    val bedtimeLabel: String,
    val doorId: Long?,
    /** A waiting or open door: its phase and its words. */
    val door: Pair<String, String>?,
    val rightNow: RightNow?,
)

/** What Habits shows. */
data class HabitsState(val openDay: String, val heatmap: HeatmapView?, val reasons: List<String>, val queued: List<HabitTick>)

/** The saved inputs both screens are worked out from. */
data class Saved(
    val nowJson: String?,
    val habits: HabitList?,
    val week: HeatmapView?,
    val year: HeatmapView?,
    val queued: List<HabitTick>,
) {
    val budget: BudgetView? get() = nowJson?.let(Budget::parse)

    companion object {
        fun read(context: Context): Saved = Saved(
            nowJson = NowCache.read(context)?.json,
            habits = HabitCache.list(context),
            week = HabitCache.heatmap(context, HabitCache.WEEK),
            year = HabitCache.heatmap(context, HabitCache.YEAR),
            queued = HabitTicks.queue(context).entries(),
        )
    }
}

object HomeState {
    fun day(saved: Saved, now: Instant, zone: ZoneId): String {
        val bedtimes = HabitDay.bedtimes(saved.nowJson)
        val clock = HabitDay.dayStartClock(bedtimes, HabitDay.timelineEnd(saved.nowJson), zone)
        return HabitDay.today(now, zone, clock, saved.budget?.day)
    }

    fun today(saved: Saved, now: Instant, zone: ZoneId): TodayState {
        val bedtimes = HabitDay.bedtimes(saved.nowJson)
        val clock = HabitDay.dayStartClock(bedtimes, HabitDay.timelineEnd(saved.nowJson), zone)
        val cached = saved.budget
        val day = HabitDay.today(now, zone, clock, cached?.day)
        val forToday = cached?.takeIf { it.day == day }
        val habits = TodayHabits.list(day, cached, saved.habits, saved.queued)
        // The week strips come from the 7-day answer, or the year's when only that is saved.
        val rows = (saved.week ?: saved.year)?.habits.orEmpty().associateBy { it.id }
        val strips = habits.orEmpty().associate { habit ->
            habit.id to HabitCells.week(rows[habit.id]?.days, day, habit.done, TickQueue.doneByDay(saved.queued, habit.id))
        }
        val closeAt = HabitDay.ticksCloseAt(day, zone, clock, cached, bedtimes)
        val door = forToday?.door
        val doorWords = door?.let { TodayText.door(it, now, zone) }
        return TodayState(
            day = day,
            habits = habits,
            strips = strips,
            ticksOpen = closeAt == null || now.isBefore(closeAt),
            budget = forToday,
            budgetUnknown = cached != null && forToday == null,
            bedtimeLabel = forToday?.let { TodayText.bedtimeLabel(it, saved.nowJson?.let { json -> BlockPolicy.bedtimeUntil(json, now) }, zone) }.orEmpty(),
            doorId = door?.id?.takeIf { doorWords != null },
            door = doorWords,
            rightNow = TodayText.rightNow(saved.nowJson, zone),
        )
    }

    fun habits(saved: Saved, now: Instant, zone: ZoneId): HabitsState = HabitsState(
        openDay = day(saved, now, zone),
        heatmap = saved.year,
        reasons = saved.habits?.reasons?.takeIf { it.isNotEmpty() } ?: saved.budget?.reasons.orEmpty(),
        queued = saved.queued,
    )
}
