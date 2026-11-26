package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Test

class SavedMemoryTest {
    private val list = """{"habits":[{"id":1,"name":"walk","position":0,"weekdays":[0,1,2,3,4,5,6]}],"reasons":["to sleep"]}"""
    private val year = """{"habits":[{"id":1,"name":"walk","created_day":"2026-01-01","days":{"2026-11-25":"done"},"current_run":1,"longest_run":3}],"combined":{"2026-11-25":1}}"""

    @Test
    fun nothingChangedGivesBackTheSameState() {
        val memory = SavedMemory()
        val first = memory.update(null, list, null, year, emptyList(), emptyList())
        // The same text read again, as a fresh copy from the preferences.
        val second = memory.update(null, String(list.toCharArray()), null, String(year.toCharArray()), emptyList(), emptyList())
        assertSame(first, second)
        assertSame(first, memory.current)
    }

    @Test
    fun theYearIsReadAgainOnlyWhenItsTextChanged() {
        val memory = SavedMemory()
        val first = memory.update(null, list, null, year, emptyList(), emptyList())
        val renamed = memory.update(null, list.replace("walk", "walk far"), null, year, emptyList(), emptyList())
        assertNotSame(first, renamed)
        assertSame(first.year, renamed.year)
        assertEquals("walk far", renamed.habits!!.active.single().name)
    }

    @Test
    fun waitingEditsAreShownOnTop() {
        val memory = SavedMemory()
        val edit = HabitEdit("k", HabitEdit.CHANGE, 1, name = "run")
        val saved = memory.update(null, list, null, null, emptyList(), listOf(edit))
        assertEquals("run", saved.habits!!.active.single().name)
        assertEquals(listOf(edit), saved.edits)
        // Once sent, the edit is gone and the server's list says the same.
        val after = memory.update(null, list.replace("walk", "run"), null, null, emptyList(), emptyList())
        assertEquals("run", after.habits!!.active.single().name)
    }

    @Test
    fun aRemovedAnswerIsForgotten() {
        val memory = SavedMemory()
        memory.update(null, list, null, year, emptyList(), emptyList())
        val cleared = memory.update(null, null, null, null, emptyList(), emptyList())
        assertNull(cleared.habits)
        assertNull(cleared.year)
    }

    @Test
    fun habitsShowsTheEditedNamesAndReasons() {
        val memory = SavedMemory()
        val saved = memory.update(null, list, null, year, emptyList(), listOf(
            HabitEdit("a", HabitEdit.CHANGE, 1, name = "run"),
            HabitEdit("b", HabitEdit.REASONS, reasons = emptyList()),
        ))
        val state = HomeState.habits(saved, java.time.Instant.parse("2026-11-26T12:00:00Z"), java.time.ZoneId.of("Europe/Istanbul"))
        assertEquals("run", state.heatmap!!.habits.single().name)
        assertEquals(emptyList<String>(), state.reasons)
        assertEquals(listOf("run"), state.habits!!.map { it.name })
    }
}
