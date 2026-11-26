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
data class HabitsState(
    val openDay: String,
    val heatmap: HeatmapView?,
    val reasons: List<String>,
    val queued: List<HabitTick>,
    /** The active habits in their order, with waiting edits on top; null when never saved. */
    val habits: List<HabitDef>? = null,
)

/** The saved inputs both screens are worked out from. */
data class Saved(
    val nowJson: String?,
    /** The server's habit list with the waiting edits on top. */
    val habits: HabitList?,
    val week: HeatmapView?,
    val year: HeatmapView?,
    val queued: List<HabitTick>,
    val edits: List<HabitEdit> = emptyList(),
) {
    val budget: BudgetView? by lazy(LazyThreadSafetyMode.PUBLICATION) { nowJson?.let(Budget::parse) }

    companion object {
        fun read(context: Context): Saved = HomeMemory.read(context)
    }
}

/**
 * The saved answers kept in memory for the life of the app, so Today and
 * Habits show at once on every visit, even after the screen is made again.
 * Each answer is read from its text again only when the text changed, and an
 * update that changes nothing gives back the same [Saved], so nothing on the
 * screen is redrawn for it.
 */
class SavedMemory {
    private class Memo<T>(private val parse: (String) -> T?) {
        private var raw: String? = null
        private var value: T? = null

        fun get(text: String?): T? {
            if (text == null) {
                raw = null
                value = null
            } else if (text != raw) {
                value = parse(text)
                raw = text
            }
            return value
        }
    }

    private val list = Memo(HabitJson::list)
    private val week = Memo(HabitJson::heatmap)
    private val year = Memo(HabitJson::heatmap)

    @Volatile var current: Saved? = null
        private set

    @Synchronized
    fun update(nowJson: String?, listJson: String?, weekJson: String?, yearJson: String?, queued: List<HabitTick>, edits: List<HabitEdit>): Saved {
        val next = Saved(nowJson, EditQueue.apply(list.get(listJson), edits), week.get(weekJson), year.get(yearJson), queued, edits)
        val before = current
        if (before != null && before == next) return before
        current = next
        return next
    }
}

/** The one [SavedMemory] of the app's process. */
object HomeMemory {
    val memory = SavedMemory()

    /** The saved answers, from memory where they have not changed. */
    fun read(context: Context): Saved = HabitEdits.queue(context).read { edits ->
        memory.update(
            nowJson = NowCache.read(context)?.json,
            listJson = HabitCache.read(context, HabitCache.LIST)?.json,
            weekJson = HabitCache.read(context, HabitCache.WEEK)?.json,
            yearJson = HabitCache.read(context, HabitCache.YEAR)?.json,
            queued = HabitTicks.queue(context).entries(),
            edits = edits,
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
        val habits = TodayHabits.list(day, cached, saved.habits, saved.queued)?.let { list -> withEdits(list, saved) }
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

    /**
     * Today's habits with the Habits screen's waiting edits on top: a new name
     * at once, a removed habit gone. A habit added on the phone waits for the
     * server to give it an id before it can be ticked.
     */
    private fun withEdits(list: List<TodayHabit>, saved: Saved): List<TodayHabit> {
        val defs = saved.habits?.habits?.associateBy { it.id }.orEmpty()
        val edited = saved.edits.map { it.habitId }.toSet()
        return list.mapNotNull { habit ->
            val def = defs[habit.id]
            when {
                habit.id < 0 -> null
                def == null || habit.id !in edited -> habit
                def.archived -> null
                else -> habit.copy(name = def.name)
            }
        }
    }

    fun habits(saved: Saved, now: Instant, zone: ZoneId): HabitsState {
        val list = saved.habits
        val editedReasons = saved.edits.any { it.kind == HabitEdit.REASONS }
        val names = if (saved.edits.isEmpty()) emptyMap() else list?.habits?.associate { it.id to it.name }.orEmpty()
        val heatmap = saved.year?.let { year ->
            if (names.isEmpty()) year else year.copy(habits = year.habits.map { row -> names[row.id]?.let { row.copy(name = it) } ?: row })
        }
        return HabitsState(
            openDay = day(saved, now, zone),
            heatmap = heatmap,
            reasons = if (editedReasons) list?.reasons.orEmpty() else list?.reasons?.takeIf { it.isNotEmpty() } ?: saved.budget?.reasons.orEmpty(),
            queued = saved.queued,
            habits = list?.active,
        )
    }
}
