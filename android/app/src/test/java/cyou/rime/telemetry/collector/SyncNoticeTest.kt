package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SyncNoticeTest {
    private val now = 10_000_000_000L
    private val minute = 60_000L

    @Test
    fun recordsWaitingHalfAnHourAfterTheLastSyncAreTold() {
        assertTrue(SyncNotice.due(pending = 1215, lastSyncMillis = now - 30 * minute, oldestPendingMillis = now - 5 * minute, now = now))
        assertFalse(SyncNotice.due(pending = 1215, lastSyncMillis = now - 29 * minute, oldestPendingMillis = now - 60 * minute, now = now))
    }

    @Test
    fun nothingWaitingIsNeverTold() {
        assertFalse(SyncNotice.due(pending = 0, lastSyncMillis = now - 600 * minute, oldestPendingMillis = null, now = now))
    }

    @Test
    fun aPhoneThatNeverSyncedCountsFromItsOldestRecord() {
        assertTrue(SyncNotice.due(pending = 3, lastSyncMillis = null, oldestPendingMillis = now - 31 * minute, now = now))
        assertFalse(SyncNotice.due(pending = 3, lastSyncMillis = null, oldestPendingMillis = now - minute, now = now))
        assertFalse(SyncNotice.due(pending = 3, lastSyncMillis = null, oldestPendingMillis = null, now = now))
    }

    @Test
    fun onlyAnHttpsRelayWithAKeyIsSaved() {
        assertTrue(RelayAddress.acceptable("https://t.example.com", "BDQAkey"))
        assertFalse(RelayAddress.acceptable("http://t.example.com", "BDQAkey"))
        assertFalse(RelayAddress.acceptable("https://", "BDQAkey"))
        assertFalse(RelayAddress.acceptable("https://t.example.com", ""))
    }

    @Test
    fun aStallIsToldOnceEvenWhenSwipedAway() {
        assertTrue(SyncNotice.postsNow("2026-11-22T08:53:00Z", null))
        assertFalse(SyncNotice.postsNow("2026-11-22T08:53:00Z", "2026-11-22T08:53:00Z"))
        // After a sync the next stall is a new one.
        assertTrue(SyncNotice.postsNow("2026-11-22T16:19:00Z", "2026-11-22T08:53:00Z"))
    }

    @Test
    fun theNoisyKindsStartOff() {
        // A habit's place in the morning starts on: the owner asked to be asked (2026-11-26).
        assertEquals(listOf(NoticeKind.SYNC, NoticeKind.FOLLOW_UPS, NoticeKind.HABIT_PLACES), NoticeKind.entries.filter { it.defaultOn })
    }
}
