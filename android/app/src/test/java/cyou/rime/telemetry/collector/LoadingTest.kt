package cyou.rime.telemetry.collector

import cyou.rime.telemetry.collector.ui.staleLine
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class LoadingTest {
    private val zone = ZoneId.of("Europe/Istanbul")
    private val now = Instant.parse("2026-11-27T11:20:00Z")

    private fun waiting() = Shown<String>(null, null, Refresh.Never, loading = true)

    @Test
    fun nothingSavedAndNothingBeforeIsThePlaceholder() {
        assertEquals(Showing.Placeholder, LoadState.of(waiting(), null))
        // Not asked yet counts as waiting too, never as an empty answer.
        assertEquals(Showing.Placeholder, LoadState.of(Shown<String>(null, null, Refresh.Never), null))
    }

    @Test
    fun anotherDayOnTheScreenIsDimmedWhileTheNewOneComes() {
        assertEquals(Showing.Dimmed("Thursday"), LoadState.of(waiting(), "Thursday"))
    }

    @Test
    fun aSavedOrFreshAnswerShowsAtOnce() {
        val saved = Shown("Friday", now.minusSeconds(3600), Refresh.Never, loading = true)
        assertEquals(Showing.Ready("Friday"), LoadState.of(saved, "Thursday"))
        assertEquals(Showing.Ready("Friday"), LoadState.of(Shown("Friday", now, Refresh.Fresh), null))
        // A saved copy stays while the server can't be reached.
        assertEquals(Showing.Ready("Friday"), LoadState.of(Shown("Friday", now, Refresh.Unreachable), null))
    }

    @Test
    fun aFailedTryWithNothingSavedIsTheFailureLine() {
        assertEquals(Showing.Failed, LoadState.of(Shown<String>(null, null, Refresh.Unreachable), "Thursday"))
        assertEquals(Showing.Failed, LoadState.of(Shown<String>(null, null, Refresh.Refused("no")), null))
    }

    @Test
    fun waitingHasNoWordsAndAnOldCopySaysWhenItIsFrom() {
        assertNull(staleLine(waiting(), now, zone))
        assertNull(staleLine(Shown("Friday", now.minusSeconds(60), Refresh.Never, loading = true), now, zone))
        assertEquals("saved 13:20", staleLine(Shown("Friday", now.minusSeconds(3600), Refresh.Never, loading = true), now, zone))
        assertNull(staleLine(Shown("Friday", now, Refresh.Fresh), now, zone))
        assertEquals("Nothing saved yet · the server can't be reached", staleLine(Shown<String>(null, null, Refresh.Unreachable), now, zone))
    }
}

class DaySwipeTest {
    private val threshold = 100f

    @Test
    fun leftIsTheNextDayAndRightTheDayBefore() {
        assertEquals("2026-11-26", DaySwipe.target("2026-11-25", "2026-11-27", -150f, threshold))
        assertEquals("2026-11-24", DaySwipe.target("2026-11-25", "2026-11-27", 150f, threshold))
        assertEquals("2026-12-01", DaySwipe.target("2026-11-30", "2026-12-05", -100f, threshold))
    }

    @Test
    fun aShortSwipeDoesNothing() {
        assertNull(DaySwipe.target("2026-11-25", "2026-11-27", -99f, threshold))
        assertNull(DaySwipe.target("2026-11-25", "2026-11-27", 40f, threshold))
    }

    @Test
    fun neverPastToday() {
        assertNull(DaySwipe.target("2026-11-27", "2026-11-27", -300f, threshold))
        assertEquals("2026-11-26", DaySwipe.target("2026-11-27", "2026-11-27", 300f, threshold))
    }

    @Test
    fun theNeighboursLeaveOutTheFuture() {
        assertEquals(listOf("2026-11-24", "2026-11-26"), DaySwipe.neighbours("2026-11-25", "2026-11-27"))
        assertEquals(listOf("2026-11-26"), DaySwipe.neighbours("2026-11-27", "2026-11-27"))
    }
}
