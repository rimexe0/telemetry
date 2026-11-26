package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class WidgetModelTest {
    private val istanbul = ZoneId.of("Europe/Istanbul")

    // Wednesday 25 November: bedtime 23:00 to 08:00 local (20:00 to 05:00 UTC); 93 minutes left.
    private fun now(day: String, habits: String, enabled: Boolean = true) = """
        {"budget":{"enabled":$enabled,"day":"$day","budget":150,"left":93,"spent":57,"earned":30,
          "rate":{"total":3,"base":3,"grayscale_off":0,"kind":"day"},
          "bedtime_at":"${day}T20:00:00Z","habits":$habits},
         "timeline":[
          {"from":"2026-11-25T08:50:00Z","to":"2026-11-25T20:00:00Z","decision":{"mode":"closed"}},
          {"from":"2026-11-25T20:00:00Z","to":"2026-11-26T05:00:00Z","decision":{"mode":"bedtime"}},
          {"from":"2026-11-26T05:00:00Z","to":"2026-11-26T20:00:00Z","decision":{"mode":"closed"}},
          {"from":"2026-11-26T20:00:00Z","to":"2026-11-27T05:00:00Z","decision":{"mode":"bedtime"}},
          {"from":"2026-11-27T05:00:00Z","to":"2026-11-27T09:00:00Z","decision":{"mode":"closed"}}]}
    """.trimIndent()

    private val five = """[
        {"id":1,"name":"Walk","done":true,"value":30},{"id":2,"name":"Gym","done":true,"value":30},
        {"id":3,"name":"Read","done":false,"value":15},{"id":4,"name":"Stretch","done":false,"value":0.4},
        {"id":5,"name":"Cook","done":false,"value":null}]"""

    private val list = HabitJson.list("""
        {"habits":[
          {"id":1,"name":"Walk","position":0,"weekdays":[]},
          {"id":2,"name":"Gym","position":1,"weekdays":[1,3,5]},
          {"id":3,"name":"Read","position":2,"weekdays":[4]}],
         "reasons":[]}
    """.trimIndent())!!

    private val wednesdayNoon = Instant.parse("2026-11-25T09:00:00Z")

    private fun tick(id: Long, day: String, done: Boolean) =
        HabitTick("k$id$day$done", id, day, Instant.parse("2026-11-25T08:00:00Z"), if (done) HabitTick.TICK else HabitTick.UNTICK)

    @Test
    fun theHeaderCountsTheHabitsAndTheTimeLeft() {
        val state = WidgetModel.state(Saved(now("2026-11-25", five), list, null, null, emptyList()), wednesdayNoon, wednesdayNoon, istanbul)
        assertEquals("Habits 2 of 5 · 1 h 33 min left", state.header)
        // Ticked habits leave the list; they are unticked in the app.
        assertEquals(listOf("Read", "Stretch", "Cook"), state.rows!!.map { it.name })
        assertEquals(listOf("+15", "+<1", ""), state.rows!!.map { it.worth })
        assertTrue(state.rows!!.all { it.week.size == 7 })
        assertTrue(state.ticksOpen)
        assertNull(state.empty)
        assertNull(state.saved)
    }

    @Test
    fun withTheBudgetOffTheHeaderHasNoTimeAndNoWorth() {
        val state = WidgetModel.state(Saved(now("2026-11-25", five, enabled = false), list, null, null, emptyList()), wednesdayNoon, wednesdayNoon, istanbul)
        assertEquals("Habits 2 of 5", state.header)
        assertTrue(state.rows!!.all { it.worth.isEmpty() })
    }

    @Test
    fun queuedTicksAreOnTheRowsAndInTheCount() {
        val queued = listOf(tick(3, "2026-11-25", true), tick(1, "2026-11-25", false), tick(1, "2026-11-25", true), tick(2, "2026-11-25", false),
            // Another day's tick changes nothing today.
            tick(5, "2026-11-24", true))
        val state = WidgetModel.state(Saved(now("2026-11-25", five), list, null, null, queued), wednesdayNoon, wednesdayNoon, istanbul)
        // Walk and Read are ticked, so only Gym, Stretch and Cook are left to tick.
        assertEquals(listOf("Gym", "Stretch", "Cook"), state.rows!!.map { it.name })
        assertEquals("Habits 2 of 5 · 1 h 33 min left", state.header)
    }

    @Test
    fun aTapTicksOrUnticksAgainstWhatIsQueued() {
        val saved = Saved(now("2026-11-25", five), list, null, null, emptyList())
        assertEquals("2026-11-25" to true, WidgetModel.tap(saved, 3, wednesdayNoon, istanbul))
        assertEquals("2026-11-25" to false, WidgetModel.tap(saved, 1, wednesdayNoon, istanbul))
        // A second tap after the first was queued unticks it again.
        val afterTap = saved.copy(queued = listOf(tick(3, "2026-11-25", true)))
        assertEquals("2026-11-25" to false, WidgetModel.tap(afterTap, 3, wednesdayNoon, istanbul))
        // Not one of today's habits, or after bedtime has started: nothing.
        assertNull(WidgetModel.tap(saved, 99, wednesdayNoon, istanbul))
        assertNull(WidgetModel.tap(saved, 3, Instant.parse("2026-11-25T20:30:00Z"), istanbul))
    }

    @Test
    fun aNewDayWithTheServerDownComesFromTheSavedHabits() {
        val saved = Saved(now("2026-11-25", five), list, null, null, listOf(tick(3, "2026-11-26", true)))
        val savedAt = Instant.parse("2026-11-25T19:10:00Z")
        // Thursday 10:00 local: Walk every day and Thursday's Read, Read ticked from the queue.
        val state = WidgetModel.state(saved, savedAt, Instant.parse("2026-11-26T07:00:00Z"), istanbul)
        assertEquals(listOf("Walk"), state.rows!!.map { it.name })
        // Today's budget is unknown, so no time left; worth unknown too.
        assertEquals("Habits 1 of 2", state.header)
        assertTrue(state.rows!!.all { it.worth.isEmpty() })
        assertEquals("saved 25 Nov 22:10", state.saved)
        assertTrue(state.ticksOpen)
    }

    @Test
    fun theSavedTimeShowsOnlyOnceTheCopyIsOld() {
        val at = Instant.parse("2026-11-25T11:20:00Z")
        assertNull(WidgetModel.savedLine(at, at.plusSeconds(14 * 60), istanbul))
        assertEquals("saved 14:20", WidgetModel.savedLine(at, at.plusSeconds(15 * 60), istanbul))
        assertNull(WidgetModel.savedLine(null, at, istanbul))
    }

    @Test
    fun nothingSavedAndNoHabitsAndBedtimeSayWhy() {
        val unknown = WidgetModel.state(Saved(null, null, null, null, emptyList()), null, wednesdayNoon, istanbul)
        assertEquals("Habits", unknown.header)
        assertNull(unknown.rows)
        assertEquals("Unknown: today's habits were never saved on this phone.", unknown.empty)

        val none = WidgetModel.state(Saved(now("2026-11-25", "[]"), list, null, null, emptyList()), wednesdayNoon, wednesdayNoon, istanbul)
        assertEquals("Habits · 1 h 33 min left", none.header)
        assertEquals("No habits scheduled today.", none.empty)

        val bedtime = Instant.parse("2026-11-25T20:30:00Z")
        val closed = WidgetModel.state(Saved(now("2026-11-25", five), list, null, null, emptyList()), bedtime, bedtime, istanbul)
        assertFalse(closed.ticksOpen)
        assertEquals("Bedtime has started, so today's habits are closed.", closed.empty)
    }

    @Test
    fun theWidgetIsDrawnAgainAtBedtimeAndAtTheNextDayStart() {
        val saved = Saved(now("2026-11-25", five), list, null, null, emptyList())
        // Wednesday noon: next is Wednesday's bedtime, when ticks close.
        assertEquals(Instant.parse("2026-11-25T20:00:00Z"), WidgetModel.nextChange(saved, wednesdayNoon, istanbul))
        // During bedtime: the next day start, 08:00 local.
        assertEquals(Instant.parse("2026-11-26T05:00:00Z"), WidgetModel.nextChange(saved, Instant.parse("2026-11-25T21:00:00Z"), istanbul))
    }

    @Test
    fun whenEveryHabitIsTickedTheWidgetSaysSo() {
        val queued = listOf(tick(3, "2026-11-25", true), tick(4, "2026-11-25", true), tick(5, "2026-11-25", true))
        val state = WidgetModel.state(Saved(now("2026-11-25", five), list, null, null, queued), wednesdayNoon, wednesdayNoon, istanbul)
        assertEquals(emptyList<WidgetRow>(), state.rows)
        assertEquals("All of today's habits are done.", state.empty)
        assertEquals("Habits 5 of 5 · 1 h 33 min left", state.header)
    }
}
