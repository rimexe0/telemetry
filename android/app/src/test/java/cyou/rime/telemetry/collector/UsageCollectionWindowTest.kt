package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

class UsageCollectionWindowTest {
    @Test fun firstRunAndClockRollbackUseOneDay() {
        assertEquals(113_600_000L, UsageCollectionWindow.begin(200_000_000L, 0L))
        assertEquals(113_600_000L, UsageCollectionWindow.begin(200_000_000L, 300_000_000L))
    }

    @Test fun resumeRetainsOfflineGapAndOverlap() {
        assertEquals(9_880_000L, UsageCollectionWindow.begin(200_000_000L, 10_000_000L))
    }
}
