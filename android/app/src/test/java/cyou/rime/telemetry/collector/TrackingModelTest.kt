package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class TrackingModelTest {
    private fun now(state: String, focus: String = "") = """{"tasks":[{"id":3,"title":"Dad's album"},{"id":4,"title":"Science fair presentation"}],
        "tracking":{"state":"$state","agent_running":false,"your_turn":[{"task_id":3,"task":"Dad's album","since":"2026-10-02T12:00:00Z"}]$focus}}"""

    @Test
    fun readsTasksTurnsAndFocus() {
        val view = TrackingModel.parse(now("other", ""","focus":{"task_id":4,"task":"Science fair presentation","next_action":"Outline the three sections",
            "since":"2026-10-02T12:00:00Z","on":"off","context":"trendyol.com","off_since":"2026-10-02T12:30:00Z"}"""))
        assertEquals(2, view.tasks.size)
        assertEquals(TurnRef(3, "Dad's album", Instant.parse("2026-10-02T12:00:00Z")), view.yourTurn[0])
        assertEquals("Outline the three sections", view.focus!!.nextAction)
        assertEquals("Away from it for 4 min · trendyol.com", TrackingModel.focusLine(view.focus!!, Instant.parse("2026-10-02T12:34:10Z")))
        assertEquals("On it", TrackingModel.focusLine(view.focus!!.copy(on = "on"), Instant.now()))
        assertNull(TrackingModel.parse(now("other")).focus)
        assertEquals("away", TrackingModel.parse("{}").state)
    }

    @Test
    fun yourTurnIsSaidOnThePhoneOnlyWhenAwayFromTheMacAndOnlyOnce() {
        val away = TrackingModel.parse(now("away"))
        assertEquals(1, TurnWatch.due(away, emptySet()).size)
        assertTrue(TurnWatch.due(away, setOf(TurnWatch.key(away.yourTurn[0]))).isEmpty())
        // At the Mac, the Mac says it.
        assertTrue(TurnWatch.due(TrackingModel.parse(now("other")), emptySet()).isEmpty())
    }
}
