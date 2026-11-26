package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class HabitCellsTest {
    private val days = mapOf(
        "2026-11-20" to "none",
        "2026-11-21" to "done",
        "2026-11-22" to "missed",
        "2026-11-23" to "unscheduled",
        // The 24th has no record.
        "2026-11-25" to "done",
    )

    @Test
    fun theWeekStripShowsEachKindOfDay() {
        val cells = HabitCells.week(days, "2026-11-26", todayDone = false)
        assertEquals((20..26).map { "2026-11-$it" }, cells.map { it.day })
        assertEquals(listOf(CellState.NONE, CellState.DONE, CellState.MISSED, CellState.UNSCHEDULED, CellState.UNKNOWN, CellState.DONE, CellState.OPEN),
            cells.map { it.state })
        // Today alone is outlined; the letters run Friday to Thursday.
        assertEquals(listOf(false, false, false, false, false, false, true), cells.map { it.today })
        assertEquals("FSSMTWT", cells.map { it.letter }.joinToString(""))
    }

    @Test
    fun todayFollowsTheTickAndAnUnsentTickCountsOnEarlierDays() {
        assertEquals(CellState.DONE, HabitCells.week(days, "2026-11-26", todayDone = true).last().state)
        // Not scheduled today stays so until ticked.
        assertEquals(CellState.UNSCHEDULED, HabitCells.week(mapOf("2026-11-26" to "unscheduled"), "2026-11-26", todayDone = false).last().state)
        // Yesterday ticked while offline and not sent yet.
        val cells = HabitCells.week(days, "2026-11-26", todayDone = false, queuedDone = mapOf("2026-11-24" to true))
        assertEquals(CellState.DONE, cells[4].state)
        // Without any record every earlier day is unknown, never missed.
        assertEquals(List(6) { CellState.UNKNOWN }, HabitCells.week(null, "2026-11-26", false).dropLast(1).map { it.state })
    }

    @Test
    fun theYearKeepsTodayOpenAndMissingDaysUnknown() {
        assertEquals(CellState.OPEN, HabitCells.year(null, "2026-11-26", "2026-11-26"))
        assertEquals(CellState.OPEN, HabitCells.year("missed", "2026-11-26", "2026-11-26"))
        assertEquals(CellState.DONE, HabitCells.year("done", "2026-11-26", "2026-11-26"))
        assertEquals(CellState.UNKNOWN, HabitCells.year(null, "2026-11-20", "2026-11-26"))
        assertEquals(listOf(-1, 0, 1, 2, 3, 4), listOf(null, 0.0, 0.2, 0.5, 0.9, 1.0).map { HabitCells.level(it) })
    }

    @Test
    fun runsAreFactsAndNeverBroken() {
        assertEquals("No runs yet", HabitCells.runs(0, 0))
        assertEquals("5 days in a row, the longest so far", HabitCells.runs(5, 5))
        assertEquals("1 day in a row · longest 9 days", HabitCells.runs(1, 9))
        assertEquals("Longest run 9 days", HabitCells.runs(0, 9))
    }

    @Test
    fun theYearGridHasMondayFirstColumns() {
        val layout = HabitCells.layout("2026-11-26", 371)
        // Thursday the 26th sits in the last column's fourth row, the rest empty.
        assertEquals(listOf("2026-11-23", "2026-11-24", "2026-11-25", "2026-11-26", null, null, null), layout.weeks.last())
        assertEquals(371, layout.weeks.flatten().count { it != null })
        assertEquals("Nov", layout.months.last().first)
    }

    @Test
    fun theWorthAndTheSavedLine() {
        assertEquals("+30", HabitCells.worth(30.0))
        assertEquals("+<1", HabitCells.worth(0.4))
        assertEquals("", HabitCells.worth(null))
        val zone = ZoneId.of("Europe/Istanbul")
        val now = Instant.parse("2026-11-26T12:00:00Z")
        assertEquals("Saved at 14:20 · the server can't be reached", TodayText.saved(Instant.parse("2026-11-26T11:20:00Z"), now, zone))
        assertEquals("Saved 25 Nov at 22:05 · the server can't be reached", TodayText.saved(Instant.parse("2026-11-25T19:05:00Z"), now, zone))
    }
}
