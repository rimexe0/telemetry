package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class FollowUpWatchTest {
    private val due = Instant.parse("2026-10-01T16:10:00Z")

    @Test
    fun readsOpenFollowUps() {
        val items = FollowUpWatch.parse("""{"follow_ups":[
            {"id":3,"title":"Hang the laundry","due_at":"2026-10-01T16:10:00Z","nag_minutes":10,"state":"open"},
            {"id":4,"title":"Call the bank","due_at":"2026-10-02T06:00:00Z","state":"open"}]}""")
        assertEquals(2, items.size)
        assertEquals(10, items[0].nagMinutes)
        assertNull(items[1].nagMinutes)
        assertTrue(FollowUpWatch.parse("""{"decision":{}}""").isEmpty())
    }

    @Test
    fun alertsOnceWhenDueAndAgainOnlyWhenAskedTo() {
        val once = FollowUp(1, "Hang the laundry", due, null)
        assertFalse(FollowUpWatch.alertsNow(once, due.minusSeconds(60), null))
        assertTrue(FollowUpWatch.alertsNow(once, due, null))
        assertFalse(FollowUpWatch.alertsNow(once, due.plusSeconds(3600), due))

        val nagging = once.copy(nagMinutes = 10)
        assertFalse(FollowUpWatch.alertsNow(nagging, due.plusSeconds(9 * 60), due))
        assertTrue(FollowUpWatch.alertsNow(nagging, due.plusSeconds(10 * 60), due))
    }
}
