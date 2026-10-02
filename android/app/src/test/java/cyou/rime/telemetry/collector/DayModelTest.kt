package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class DayModelTest {
    private val zone = ZoneId.of("Europe/Istanbul")
    private val json = """{"date":"2026-10-01","from":"2026-09-30T21:00:00Z","to":"2026-10-01T10:30:00Z","plan_exists":true,"day":{
        "blocks":[{"id":1,"title":"Telemetry","start":"2026-10-01T09:00:00Z","end":"2026-10-01T10:00:00Z","state":"planned","closes_distractions":true,"status":"past","tracked":true,"in_plan_minutes":25.5,"distraction_minutes":34.5},
                  {"id":2,"title":"Later","start":"2026-10-01T12:00:00Z","end":"2026-10-01T13:00:00Z","state":"planned","closes_distractions":false,"status":"upcoming","in_plan_minutes":0,"distraction_minutes":0}],
        "tasks":[{"id":3,"title":"Dad's album","tracked":true,"minutes":25.5,"agent_minutes":25,"waiting_minutes":30,"your_turn_since":"2026-10-01T10:00:00Z"},{"id":4,"title":"Science fair presentation","tracked":false,"minutes":0}],
        "planned_minutes":120,"in_plan_minutes":25.5,"distraction_minutes":34.5,
        "distraction_by_device":[{"device":"mac","minutes":15},{"device":"phone","minutes":19.5}],
        "chunks":[{"device":"phone","start":"2026-10-01T06:20:00Z","end":"2026-10-01T06:40:00Z","kind":"distraction","active_minutes":20,"distraction_minutes":19.5,
                   "items":[{"name":"com.twitter.android","label":"distraction","minutes":19.5,"visits":2},{"name":"com.whatsapp","label":"unlabeled","minutes":0.5,"visits":1}],
                   "segments":[{"start":"2026-10-01T06:20:00Z","end":"2026-10-01T06:30:00Z","name":"com.twitter.android","label":"distraction"}]}],
        "no_data":[{"device":"phone","start":"2026-10-01T07:00:00Z","end":"2026-10-01T08:00:00Z"}]}}"""

    @Test
    fun readsThePlanTotalsAndChunks() {
        val view = DayModel.parse(json)
        assertEquals(2, view.blocks.size)
        assertEquals(25.5, view.blocks[0].inPlanMinutes, 0.0)
        assertEquals("upcoming", view.blocks[1].status)
        assertEquals(34.5, view.distractionMinutes, 0.0)
        assertEquals(listOf("mac" to 15.0, "phone" to 19.5), view.distractionByDevice)
        assertEquals(DayModel.DISTRACTION, view.chunks[0].kind)
        assertTrue(view.blocks[0].tracked)
        assertFalse(view.blocks[1].tracked)
        assertEquals(2, view.chunks[0].items.size)
        assertEquals(1, view.chunks[0].segments.size)
        assertEquals(1, view.gaps.size)
        assertEquals(listOf(DayTask(3, "Dad's album", true, 25.5, 25.0, 30.0, Instant.parse("2026-10-01T10:00:00Z")), DayTask(4, "Science fair presentation", false, 0.0)), view.tasks)
    }

    @Test
    fun groupsTheSameThingAcrossChunksAndKeepsThreadsApart() {
        fun item(name: String, detail: String?, minutes: Double, device: String = "mac") = DayItem(name, detail, "unlabeled", minutes, 1, device)
        fun chunk(vararg items: DayItem) = DayChunk("mac", Instant.EPOCH, Instant.EPOCH, "other", null, 0.0, items.toList(), emptyList())
        val grouped = DayModel.grouped(listOf(
            chunk(item("T3 Code", "Telemetry · Fix collector", 10.0), item("Code", null, 4.0)),
            chunk(item("T3 Code", "Telemetry · Fix collector", 15.0), item("T3 Code", "Telemetry · Phone app", 12.0), item("Code", null, 3.0, "phone")),
        ))
        assertEquals(listOf(25.0, 12.0, 4.0, 3.0), grouped.map { it.minutes })
        assertEquals("Telemetry · Fix collector", grouped[0].detail)
        assertEquals(2, grouped[0].visits)
    }

    @Test
    fun timelineSpansFirstActivityToLastBlockInWholeHours() {
        val (start, end) = DayModel.window(DayModel.parse(json), zone)
        // First chunk 09:20 local, last block ends 16:00 local.
        assertEquals(Instant.parse("2026-10-01T06:00:00Z"), start)
        assertEquals(Instant.parse("2026-10-01T13:00:00Z"), end)
    }

    @Test
    fun aFinishedDayEndsAtItsLastActivityAndIsNamed() {
        val finished = DayModel.parse(json.replace(""""to":"2026-10-01T10:30:00Z"""", """"to":"2026-10-01T21:00:00Z"""")
            .replace(""""blocks":[{""", """"blocks":[],"ignored":[{"""))
        val (start, end) = DayModel.window(finished, zone)
        // The only chunk runs 09:20-09:40 local, so the strip is 09:00-10:00, not up to midnight.
        assertEquals(Instant.parse("2026-10-01T06:00:00Z"), start)
        assertEquals(Instant.parse("2026-10-01T07:00:00Z"), end)
        val now = Instant.parse("2026-10-02T09:00:00Z")
        assertEquals("Today", DayModel.dayLabel("2026-10-02", now, zone))
        assertEquals("Yesterday", DayModel.dayLabel("2026-10-01", now, zone))
        assertEquals("Tue 29 Sep", DayModel.dayLabel("2026-09-29", now, zone))
    }

    @Test
    fun formatsDurationsAndKnowsTheDay() {
        assertEquals("30s", DayModel.clock(0.5))
        assertEquals("41m", DayModel.clock(41.2))
        assertEquals("2h", DayModel.clock(120.0))
        assertEquals("1h 12m", DayModel.clock(72.0))
        val view = DayModel.parse(json)
        assertTrue(DayModel.isToday(view, Instant.parse("2026-10-01T20:59:00Z"), zone))
        assertFalse(DayModel.isToday(view, Instant.parse("2026-10-01T21:01:00Z"), zone))
    }
}
