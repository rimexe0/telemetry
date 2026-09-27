package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class NowModelTest {
    private val json = """
        {"generated_at":"2026-09-28T07:00:00Z","plan_exists":true,
         "task":{"title":"Client work","next_action":"Draft summary","resume_marker":"Section 2","state":"active"},
         "decision":{"mode":"work","reason":"Working on Client work. Until 11:00."},
         "timeline":[
          {"from":"2026-09-28T07:00:00.123456789Z","to":"2026-09-28T08:00:00Z","decision":{"mode":"work","reason":"Working on Client work. Until 11:00."}},
          {"from":"2026-09-28T08:00:00Z","to":"2026-09-28T19:00:00Z","decision":{"mode":"leisure","reason":"Free time. Nothing is blocked."}}]}
    """.trimIndent()

    @Test
    fun liveResponseShowsTaskAndDecision() {
        val now = Instant.parse("2026-09-28T07:30:00Z")
        val view = NowModel.parse(json, now, now, offline = false)
        assertEquals("Working", view.modeLabel)
        assertEquals("Client work", view.taskTitle)
        assertEquals("Draft summary", view.nextAction)
        assertEquals("Section 2", view.resumeMarker)
        assertEquals("In progress", view.taskState)
        assertFalse(view.offline)
    }

    @Test
    fun offlineFollowsTheCachedTimeline() {
        val fetched = Instant.parse("2026-09-28T07:00:00Z")
        val view = NowModel.parse(json, fetched, Instant.parse("2026-09-28T09:00:00Z"), offline = true)
        assertEquals("Free time", view.modeLabel)
        assertTrue(view.offline)
    }

    @Test
    fun cacheBeyondTheTimelineIsNotTrusted() {
        assertTrue(NowModel.timelineCovers(json, Instant.parse("2026-09-28T18:59:00Z")))
        assertFalse(NowModel.timelineCovers(json, Instant.parse("2026-09-28T19:00:00Z")))
    }

    @Test
    fun missingTaskIsNotInvented() {
        val view = NowModel.parse("""{"plan_exists":false,"task":null,"decision":{"mode":"plan_gate","reason":"No plan"}}""", Instant.EPOCH, Instant.EPOCH, offline = false)
        assertNull(view.taskTitle)
        assertEquals("Plan your day", view.modeLabel)
        assertFalse(view.planExists)
    }

    @Test
    fun ageTextIsReadable() {
        val base = Instant.parse("2026-09-28T07:00:00Z")
        assertEquals("just now", NowModel.ageText(base, base.plusSeconds(20)))
        assertEquals("5 min ago", NowModel.ageText(base, base.plusSeconds(300)))
        assertEquals("2 h ago", NowModel.ageText(base, base.plusSeconds(7300)))
    }
}
