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
        "blocks":[{"id":1,"title":"Telemetry","start":"2026-10-01T09:00:00Z","end":"2026-10-01T10:00:00Z","state":"planned","closes_distractions":true,"status":"past","in_plan_minutes":25.5,"distraction_minutes":34.5},
                  {"id":2,"title":"Later","start":"2026-10-01T12:00:00Z","end":"2026-10-01T13:00:00Z","state":"planned","closes_distractions":false,"status":"upcoming","in_plan_minutes":0,"distraction_minutes":0}],
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
        assertTrue(view.chunks[0].distraction)
        assertEquals(2, view.chunks[0].items.size)
        assertEquals(1, view.chunks[0].segments.size)
        assertEquals(1, view.gaps.size)
    }

    @Test
    fun timelineSpansFirstActivityToLastBlockInWholeHours() {
        val (start, end) = DayModel.window(DayModel.parse(json), zone)
        // First chunk 09:20 local, last block ends 16:00 local.
        assertEquals(Instant.parse("2026-10-01T06:00:00Z"), start)
        assertEquals(Instant.parse("2026-10-01T13:00:00Z"), end)
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
