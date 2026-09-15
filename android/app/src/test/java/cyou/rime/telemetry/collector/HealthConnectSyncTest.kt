package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

/** Contract tests for the states exposed by the scheduled health sync. */
class HealthConnectSyncTest {
    @Test
    fun zeroRecordsAreReportedAsEmpty() {
        assertEquals(HealthConnectSync.HealthSyncState.EMPTY, HealthConnectSync.HealthSyncState.forRecordCount(0))
        assertEquals(HealthConnectSync.HealthSyncState.EMPTY, HealthConnectSync.HealthSyncState.initialReadState(0))
    }

    @Test
    fun anyRecordIsReportedAsOk() {
        assertEquals(HealthConnectSync.HealthSyncState.OK, HealthConnectSync.HealthSyncState.forRecordCount(1))
        assertEquals(HealthConnectSync.HealthSyncState.OK, HealthConnectSync.HealthSyncState.forRecordCount(500))
        assertEquals(HealthConnectSync.HealthSyncState.RECONCILED, HealthConnectSync.HealthSyncState.initialReadState(1))
    }

    @Test
    fun emptyCursorPollIsNotReportedAsEmptyHealthData() {
        assertEquals(HealthConnectSync.HealthSyncState.UNCHANGED, HealthConnectSync.HealthSyncState.changePollState(0, hasMore = false))
        assertEquals(HealthConnectSync.HealthSyncState.OK, HealthConnectSync.HealthSyncState.changePollState(1, hasMore = false))
    }

    @Test
    fun boundedCursorPollReportsRemainingPages() {
        assertEquals(HealthConnectSync.HealthSyncState.PARTIAL, HealthConnectSync.HealthSyncState.changePollState(500, hasMore = true))
    }

    @Test
    fun permissionStatesRemainDistinct() {
        assertEquals("permission_required", HealthConnectSync.HealthSyncState.PERMISSION_REQUIRED)
        assertEquals("background_permission_required", HealthConnectSync.HealthSyncState.BACKGROUND_PERMISSION_REQUIRED)
        assertEquals("unavailable", HealthConnectSync.HealthSyncState.UNAVAILABLE)
        assertEquals("error", HealthConnectSync.HealthSyncState.ERROR)
        assertEquals("reconciled", HealthConnectSync.HealthSyncState.RECONCILED)
        assertEquals("gap_reconciled", HealthConnectSync.HealthSyncState.GAP_RECONCILED)
    }
}
