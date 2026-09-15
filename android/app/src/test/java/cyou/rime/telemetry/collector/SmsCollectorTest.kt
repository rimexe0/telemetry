package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.json.JSONObject

class SmsCollectorTest {
    @Test
    fun accessStateKeepsOptInPermissionAndProviderFailuresDistinct() {
        assertEquals(SmsCollectionState.DISABLED, smsAccessState(false, false, false))
        assertEquals(SmsCollectionState.PERMISSION_REQUIRED, smsAccessState(true, false, true))
        assertEquals(SmsCollectionState.PROVIDER_UNAVAILABLE, smsAccessState(true, true, false))
        assertEquals(SmsCollectionState.OK, smsAccessState(true, true, true))
        assertEquals(SmsCollectionState.QUERY_TIMEOUT, smsErrorState(SmsQueryTimeoutException()))
        assertEquals(SmsCollectionState.PERMISSION_REQUIRED, smsErrorState(SecurityException()))
        assertEquals(SmsCollectionState.ERROR, smsErrorState(IllegalStateException()))
    }

    @Test
    fun historyPageIsBoundedAndCursorResumesAfterLastStableRow() {
        val source = RecordingSmsSource(
            history = SmsPage(
                records = listOf(record(id = 41L, date = 1_000L), record(id = 42L, date = 1_000L)),
                hasMore = true,
            ),
            refresh = SmsPage(emptyList(), hasMore = false),
        )
        val state = FakeSmsCursorStore(SmsCursor())
        val sync = SmsSync(source, state) { records -> records.size }

        val result = sync.collect(nowMillis = 10_000L)

        assertEquals(SmsCollectionState.PARTIAL, result.state)
        assertEquals(2, result.recordsRead)
        assertEquals(SmsQueryMode.HISTORY, source.queries[0].mode)
        assertEquals(SmsCollector.MAX_HISTORY_ROWS, source.queries[0].limit)
        assertEquals(SmsQueryMode.RECENT_REFRESH, source.queries[1].mode)
        assertEquals(SmsCollector.MAX_HISTORY_ROWS, source.queries[1].limit)
        assertEquals(1_000L, state.value.lastDateMillis)
        assertEquals(42L, state.value.lastId)
        assertFalse(state.value.historyComplete)

        val nextSource = RecordingSmsSource(
            history = SmsPage(listOf(record(id = 43L, date = 1_001L)), hasMore = false),
            refresh = SmsPage(emptyList(), hasMore = false),
        )
        val nextSync = SmsSync(nextSource, state) { records -> records.size }
        nextSync.collect(nowMillis = 20_000L)

        assertEquals(1_000L, nextSource.queries[0].afterDateMillis)
        assertEquals(42L, nextSource.queries[0].afterId)
        assertTrue(state.value.historyComplete)
        assertEquals(1_001L, state.value.lastDateMillis)
        assertEquals(43L, state.value.lastId)
    }

    @Test
    fun unchangedReplayAddsNoRevisionButChangedReadStateAddsOne() {
        val unchanged = record(id = 7L, date = 2_000L, read = false)
        val source = MutableSmsSource(unchanged)
        val state = FakeSmsCursorStore(SmsCursor(historyComplete = true, lastDateMillis = 2_000L, lastId = 7L))
        val payloads = linkedSetOf<String>()
        val sync = SmsSync(source, state) { records ->
            records.count { payloads.add(it.payloadJson) }
        }

        assertEquals(1, sync.collect(nowMillis = 10_000L).insertedRevisions)
        assertEquals(SmsCollectionState.UNCHANGED, sync.collect(nowMillis = 11_000L).state)

        source.record = unchanged.copy(read = 1, seen = 1)
        val changed = sync.collect(nowMillis = 12_000L)
        assertEquals(1, changed.insertedRevisions)
        assertEquals(SmsCollectionState.OK, changed.state)
    }

    @Test
    fun emptyInitialInboxIsDifferentFromAnUnchangedPoll() {
        val state = FakeSmsCursorStore(SmsCursor())
        val source = RecordingSmsSource(
            history = SmsPage(emptyList(), hasMore = false),
            refresh = SmsPage(emptyList(), hasMore = false),
        )
        val sync = SmsSync(source, state) { records -> records.size }

        assertEquals(SmsCollectionState.EMPTY_INITIAL, sync.collect(nowMillis = 10_000L).state)
        assertEquals(SmsCollectionState.UNCHANGED, sync.collect(nowMillis = 11_000L).state)
    }

    @Test
    fun observationKeepsSourceFieldsWithoutMakingCollectionTimePartOfRevision() {
        val source = RecordingSmsSource(
            history = SmsPage(listOf(record(id = 9L, date = 3_000L)), hasMore = false),
            refresh = SmsPage(emptyList(), hasMore = false),
        )
        var captured: LocalObservation? = null
        val sync = SmsSync(source, FakeSmsCursorStore(SmsCursor())) { records ->
            captured = records.single()
            records.size
        }

        sync.collect(nowMillis = 10_000L)

        val observation = requireNotNull(captured)
        assertEquals("android_sms", observation.sourceKind)
        assertEquals("android.sms.inbox", observation.kind)
        assertEquals("sms:9:3000", observation.sourceRecordKey)
        assertEquals("1970-01-01T00:00:03Z", observation.occurredAt)
        val payload = JSONObject(observation.payloadJson)
        assertEquals("bank", payload.getString("address"))
        assertEquals("retained body", payload.getString("body"))
        assertEquals(3_000L, payload.getLong("date_millis"))
        assertEquals(3_000L, payload.getLong("date_sent_millis"))
        assertEquals(0, payload.getInt("read"))
        assertFalse(payload.has("collected_at"))
    }

    @Test
    fun invalidProviderDateIsReportedAndDoesNotStallTheCursor() {
        val state = FakeSmsCursorStore(SmsCursor())
        val source = RecordingSmsSource(
            history = SmsPage(listOf(record(id = 12L, date = -100L)), hasMore = false, invalidRows = 1),
            refresh = SmsPage(emptyList(), hasMore = false),
        )
        val sync = SmsSync(source, state) { records -> records.size }

        val result = sync.collect(nowMillis = 10_000L)

        assertEquals(SmsCollectionState.INVALID_ROWS, result.state)
        assertEquals(1, result.invalidRows)
        assertEquals(-100L, state.value.lastDateMillis)
        assertEquals(12L, state.value.lastId)
        assertTrue(state.value.historyComplete)
    }

    @Test
    fun sourceFailureDoesNotAdvanceCursor() {
        val state = FakeSmsCursorStore(SmsCursor(lastDateMillis = 5_000L, lastId = 5L))
        val source = RecordingSmsSource(history = null, refresh = SmsPage(emptyList(), false))
        val sync = SmsSync(source, state) { records -> records.size }

        val result = sync.collect(nowMillis = 10_000L)

        assertEquals(SmsCollectionState.PROVIDER_UNAVAILABLE, result.state)
        assertEquals(5_000L, state.value.lastDateMillis)
        assertEquals(5L, state.value.lastId)
    }

    private fun record(
        id: Long,
        date: Long,
        read: Boolean = false,
    ) = SmsRecord(
        id = id,
        threadId = 3L,
        address = "bank",
        dateMillis = date,
        dateSentMillis = date,
        type = 1,
        status = -1,
        read = if (read) 1 else 0,
        seen = if (read) 1 else 0,
        subscriptionId = 1,
        body = "retained body",
    )

    private class FakeSmsCursorStore(var value: SmsCursor) : SmsCursorStore {
        override fun load(): SmsCursor = value
        override fun save(cursor: SmsCursor): Boolean {
            value = cursor
            return true
        }
    }

    private class RecordingSmsSource(
        private val history: SmsPage?,
        private val refresh: SmsPage,
    ) : SmsSource {
        val queries = mutableListOf<SmsQuery>()

        override fun query(query: SmsQuery): SmsPage? {
            queries += query
            return if (query.mode == SmsQueryMode.HISTORY) history else refresh
        }
    }

    private class MutableSmsSource(var record: SmsRecord) : SmsSource {
        override fun query(query: SmsQuery): SmsPage = if (query.mode == SmsQueryMode.HISTORY) {
            SmsPage(emptyList(), hasMore = false)
        } else {
            SmsPage(listOf(record), hasMore = false)
        }
    }

}
