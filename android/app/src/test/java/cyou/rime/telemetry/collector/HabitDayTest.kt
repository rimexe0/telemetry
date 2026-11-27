package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.LocalTime
import java.time.ZoneId

class HabitDayTest {
    private val istanbul = ZoneId.of("Europe/Istanbul")

    // Wednesday 25 November: bedtime 23:00 to 08:00 local (20:00 to 05:00 UTC).
    private fun now(day: String, habits: String, bedtimeAt: String = "${day}T20:00:00Z") = """
        {"budget":{"enabled":true,"day":"$day","budget":150,"left":100,"spent":50,"earned":30,
          "rate":{"total":3,"base":3,"grayscale_off":0,"kind":"day"},
          "bedtime_at":"$bedtimeAt","habits":$habits},
         "timeline":[
          {"from":"2026-11-25T08:50:00Z","to":"2026-11-25T20:00:00Z","decision":{"mode":"closed"}},
          {"from":"2026-11-25T20:00:00Z","to":"2026-11-26T05:00:00Z","decision":{"mode":"bedtime"}},
          {"from":"2026-11-26T05:00:00Z","to":"2026-11-26T20:00:00Z","decision":{"mode":"closed"}},
          {"from":"2026-11-26T20:00:00Z","to":"2026-11-27T05:00:00Z","decision":{"mode":"bedtime"}},
          {"from":"2026-11-27T05:00:00Z","to":"2026-11-27T09:00:00Z","decision":{"mode":"closed"}}]}
    """.trimIndent()

    private val list = HabitJson.list("""
        {"habits":[
          {"id":1,"name":"Walk","position":0,"weekdays":[],"created_at":"2026-11-01T00:00:00Z"},
          {"id":2,"name":"Gym","position":1,"weekdays":[1,3,5],"created_at":"2026-11-01T00:00:00Z"},
          {"id":3,"name":"Read","position":2,"weekdays":[4],"created_at":"2026-11-01T00:00:00Z"},
          {"id":4,"name":"Old","position":3,"weekdays":[],"created_at":"2026-11-01T00:00:00Z","archived_at":"2026-11-10T00:00:00Z"},
          {"id":5,"name":"Stretch","position":-1,"weekdays":[0,4],"created_at":"2026-11-01T00:00:00Z"}],
         "reasons":["Finish the thesis"]}
    """.trimIndent())!!

    @Test
    fun theDayStartsWhereTheSavedBedtimeEnds() {
        val json = now("2026-11-25", "[]")
        val clock = HabitDay.dayStartClock(HabitDay.bedtimes(json), HabitDay.timelineEnd(json), istanbul)
        assertEquals(LocalTime.of(8, 0), clock)
        // 07:30 on the 26th is still the 25th's bedtime; 08:00 starts the 26th.
        assertEquals("2026-11-25", HabitDay.today(Instant.parse("2026-11-26T04:30:00Z"), istanbul, clock, "2026-11-25"))
        assertEquals("2026-11-26", HabitDay.today(Instant.parse("2026-11-26T05:00:00Z"), istanbul, clock, "2026-11-25"))
        // Without bedtime the day starts at 04:00.
        assertEquals(LocalTime.of(4, 0), HabitDay.dayStartClock(emptyList(), null, istanbul))
        // Never before the day the server last named.
        assertEquals("2026-11-26", HabitDay.today(Instant.parse("2026-11-26T03:00:00Z"), istanbul, clock, "2026-11-26"))
    }

    @Test
    fun aNewDayWithTheServerDownTakesItsHabitsFromTheirWeekdays() {
        val cached = Budget.parse(now("2026-11-25", """[{"id":1,"name":"Walk","done":true,"value":30},{"id":2,"name":"Gym","done":true,"value":30}]"""))
        // Thursday the 26th: every-day Walk, Thursday's Read and Stretch, in their order; not Monday's Gym, not archived Old.
        val habits = TodayHabits.list("2026-11-26", cached, list, emptyList())!!
        assertEquals(listOf("Stretch", "Walk", "Read"), habits.map { it.name })
        // Nothing ticked, and what each is worth is unknown.
        assertTrue(habits.none { it.done })
        assertTrue(habits.all { it.value == null })
    }

    @Test
    fun aNewDayCountsOnlyTodaysQueuedTicks() {
        val queue = listOf(
            HabitTick("a", 1, "2026-11-26", Instant.parse("2026-11-26T06:00:00Z"), HabitTick.TICK),
            HabitTick("b", 3, "2026-11-25", Instant.parse("2026-11-25T18:00:00Z"), HabitTick.TICK),
        )
        val habits = TodayHabits.list("2026-11-26", null, list, queue)!!
        assertEquals(mapOf(5L to false, 1L to true, 3L to false), habits.associate { it.id to it.done })
    }

    @Test
    fun theSameDayUsesTheServersOwnListWithQueuedTicksOnTop() {
        val cached = Budget.parse(now("2026-11-26", """[{"id":1,"name":"Walk","done":true,"value":30},{"id":2,"name":"Gym","done":false,"value":30}]"""))
        val queue = listOf(
            HabitTick("a", 1, "2026-11-26", Instant.parse("2026-11-26T06:00:00Z"), HabitTick.UNTICK),
            HabitTick("b", 2, "2026-11-26", Instant.parse("2026-11-26T06:01:00Z"), HabitTick.TICK),
        )
        val habits = TodayHabits.list("2026-11-26", cached, list, queue)!!
        assertEquals(listOf(
            TodayHabit(1, "Walk", false, 30.0, unticked = true),
            TodayHabit(2, "Gym", true, 30.0, doneAt = Instant.parse("2026-11-26T06:01:00Z"), doneBy = "hand"),
        ), habits)
    }

    @Test
    fun withNothingSavedTodaysHabitsAreUnknown() {
        assertNull(TodayHabits.list("2026-11-26", null, null, emptyList()))
    }

    @Test
    fun ticksCloseWhenBedtimeStarts() {
        val json = now("2026-11-25", "[]")
        val cached = Budget.parse(json)
        val bedtimes = HabitDay.bedtimes(json)
        val clock = LocalTime.of(8, 0)
        // The day the server described: its own bedtime.
        assertEquals(Instant.parse("2026-11-25T20:00:00Z"), HabitDay.ticksCloseAt("2026-11-25", istanbul, clock, cached, bedtimes))
        // A later day: the first bedtime in the timeline after that day starts.
        assertEquals(Instant.parse("2026-11-26T20:00:00Z"), HabitDay.ticksCloseAt("2026-11-26", istanbul, clock, cached, bedtimes))
        // Past the timeline the bedtime is unknown, so ticks stay allowed.
        assertNull(HabitDay.ticksCloseAt("2026-11-28", istanbul, clock, cached, bedtimes))
    }

    @Test
    fun todayOnTheScreenPutsItTogether() {
        val json = now("2026-11-25", """[{"id":1,"name":"Walk","done":true,"value":30}]""")
        val saved = Saved(json, list, null, null, emptyList())
        // Thursday 10:00 local, the server unreachable since Wednesday.
        val state = HomeState.today(saved, Instant.parse("2026-11-26T07:00:00Z"), istanbul)
        assertEquals("2026-11-26", state.day)
        assertTrue(state.budgetUnknown)
        assertNull(state.budget)
        assertTrue(state.ticksOpen)
        assertEquals(listOf("Stretch", "Walk", "Read"), state.habits!!.map { it.name })
        // After Thursday's bedtime starts, ticks close.
        assertFalse(HomeState.today(saved, Instant.parse("2026-11-26T20:30:00Z"), istanbul).ticksOpen)
        // Wednesday itself is the server's own day, with its budget.
        val wednesday = HomeState.today(saved, Instant.parse("2026-11-25T12:00:00Z"), istanbul)
        assertEquals("2026-11-25", wednesday.day)
        assertFalse(wednesday.budgetUnknown)
        assertEquals(100.0, wednesday.budget!!.left, 0.0)
    }
}
