package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

// Made-up habits for tests, not the owner's.
class HabitRowsTest {
    private fun def(id: Long, name: String, position: Int, archived: Boolean = false) = HabitDef(id, name, position, emptySet(), archived)
    private fun year(id: Long, name: String, days: Map<String, String> = emptyMap(), archivedDay: String? = null, run: Int = 0) =
        HeatmapRow(id, name, "2026-01-01", archivedDay, days, run, run)

    @Test
    fun eachHabitIsOneRowWithItsYear() {
        val list = listOf(def(2, "Read", 1), def(1, "Walk", 0))
        val heatmap = HeatmapView(listOf(year(1, "Walk", mapOf("2026-11-26" to "done"), run = 3), year(2, "Read"), year(2, "Read")), emptyMap())
        val rows = HabitRows.merge(HabitList(list, emptyList()).active, heatmap)
        assertEquals(listOf(1L, 2L), rows.map { it.id })
        assertEquals(3, rows[0].year?.currentRun)
        assertEquals("Walk", rows[0].def?.name)
        assertFalse(rows.any { it.archived })
    }

    @Test
    fun theEditedNameIsTheRowsName() {
        val rows = HabitRows.merge(listOf(def(1, "Walk outside", 0)), HeatmapView(listOf(year(1, "Walk")), emptyMap()))
        assertEquals(listOf("Walk outside"), rows.map { it.name })
    }

    @Test
    fun aNewHabitHasARowBeforeItHasAYear() {
        val rows = HabitRows.merge(listOf(def(1, "Walk", 0), def(-1, "Stretch", 1)), HeatmapView(listOf(year(1, "Walk")), emptyMap()))
        assertEquals(listOf(1L, -1L), rows.map { it.id })
        assertNull(rows[1].year)
    }

    @Test
    fun archivedHabitsComeAfterOnlyWithSomethingRecorded() {
        val heatmap = HeatmapView(listOf(
            year(1, "Walk"),
            year(3, "Old", mapOf("2026-03-01" to "missed"), archivedDay = "2026-04-01"),
            year(4, "Never used", mapOf("2026-03-01" to "none"), archivedDay = "2026-04-01"),
        ), emptyMap())
        val rows = HabitRows.merge(listOf(def(1, "Walk", 0)), heatmap)
        assertEquals(listOf(1L, 3L), rows.map { it.id })
        assertTrue(rows[1].archived)
        assertNull(rows[1].def)
    }

    @Test
    fun withoutASavedListTheYearStandsIn() {
        val heatmap = HeatmapView(listOf(year(1, "Walk"), year(3, "Old", mapOf("2026-03-01" to "done"), archivedDay = "2026-04-01")), emptyMap())
        val rows = HabitRows.merge(null, heatmap)
        assertEquals(listOf(1L, 3L), rows.map { it.id })
        assertFalse(rows[0].archived)
        assertTrue(rows[1].archived)
        assertEquals(emptyList<HabitYearRow>(), HabitRows.merge(null, null))
    }
}
