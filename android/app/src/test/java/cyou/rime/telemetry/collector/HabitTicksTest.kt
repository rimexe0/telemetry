package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class HabitTicksTest {
    /** Stands in for the phone's preferences: a string that outlives each queue. */
    private class Memory : TickStore {
        var json: String? = null
        override fun load(): String? = json
        override fun save(json: String) { this.json = json }
    }

    private val at = Instant.parse("2026-11-26T10:00:00Z")

    @Test
    fun entriesAreSentInTheOrderTheyWereMade() {
        val queue = TickQueue(Memory())
        queue.add(1, "2026-11-26", at, done = true)
        queue.add(2, "2026-11-26", at.plusSeconds(5), done = true)
        queue.add(1, "2026-11-26", at.plusSeconds(9), done = false)
        val sent = mutableListOf<String>()
        val flushed = queue.flush { sent += "${it.habitId}:${it.action}"; TickSend.Sent(null) }
        assertEquals(listOf("1:tick", "2:tick", "1:untick"), sent)
        assertEquals(3, flushed.sent)
        assertEquals(0, flushed.waiting)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun anEntryThatMustWaitKeepsItselfAndEverythingAfterIt() {
        val queue = TickQueue(Memory())
        queue.add(1, "2026-11-26", at, done = true)
        queue.add(1, "2026-11-26", at.plusSeconds(5), done = false)
        queue.add(2, "2026-11-26", at.plusSeconds(9), done = true)
        var calls = 0
        // The first goes, the second finds the server down: the untick must not be overtaken.
        val flushed = queue.flush { calls++; if (calls == 1) TickSend.Sent(null) else TickSend.Retry }
        assertEquals(2, calls)
        assertEquals(1, flushed.sent)
        assertEquals(listOf(HabitTick.UNTICK, HabitTick.TICK), queue.entries().map { it.action })

        // Next time the server answers, the rest go in order.
        val sent = mutableListOf<Long>()
        queue.flush { sent += it.habitId; TickSend.Sent(null) }
        assertEquals(listOf(1L, 2L), sent)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun aRefusalIsDroppedAndItsMessageKept() {
        val queue = TickQueue(Memory())
        queue.add(1, "2026-11-25", at, done = true)
        queue.add(2, "2026-11-26", at, done = true)
        val flushed = queue.flush { if (it.habitId == 1L) TickSend.Refused("Yesterday's habits closed at bedtime.") else TickSend.Sent(null) }
        assertEquals(listOf("Yesterday's habits closed at bedtime."), flushed.refusals)
        assertEquals(1, flushed.sent)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun theQueueOutlivesTheAppBeingClosed() {
        val store = Memory()
        TickQueue(store).add(7, "2026-11-26", at, done = true)
        TickQueue(store).add(7, "2026-11-26", at.plusSeconds(60), done = false)
        val reopened = TickQueue(store).entries()
        assertEquals(2, reopened.size)
        assertEquals(HabitTick(reopened[0].key, 7, "2026-11-26", at, HabitTick.TICK), reopened[0])
        assertEquals(at.plusSeconds(60), reopened[1].at)
        assertTrue(reopened[0].key != reopened[1].key)
    }

    @Test
    fun aDamagedStoreReadsAsEmpty() {
        val store = Memory().apply { json = "not json" }
        assertTrue(TickQueue(store).entries().isEmpty())
        store.json = """[{"key":"a","habit_id":1,"day":"2026-11-26","at":"2026-11-26T10:00:00Z","action":"explode"},
            {"key":"b","habit_id":2,"day":"2026-11-26","at":"2026-11-26T10:00:00Z","action":"tick"}]"""
        assertEquals(listOf(2L), TickQueue(store).entries().map { it.habitId })
    }

    @Test
    fun theServersAnswerDecidesWhetherToKeepIt() {
        assertEquals(TickSend.Retry, TickQueue.outcome(null))
        assertEquals(TickSend.Retry, TickQueue.outcome(DeviceApi.Answer(502, "")))
        assertEquals(TickSend.Retry, TickQueue.outcome(DeviceApi.Answer(401, "")))
        assertEquals(TickSend.Retry, TickQueue.outcome(DeviceApi.Answer(429, "")))
        val sent = TickQueue.outcome(DeviceApi.Answer(200, """{"day":"2026-11-26","left":40}""")) as TickSend.Sent
        assertEquals(40, sent.budget!!.getInt("left"))
        assertNull((TickQueue.outcome(DeviceApi.Answer(204, "")) as TickSend.Sent).budget)
        assertEquals(TickSend.Refused("Today's habits closed at bedtime."),
            TickQueue.outcome(DeviceApi.Answer(409, """{"error":"day_closed","message":"Today's habits closed at bedtime."}""")))
        assertTrue(TickQueue.outcome(DeviceApi.Answer(400, """{"error":"invalid_day"}""")) is TickSend.Refused)
    }

    @Test
    fun queuedTicksLeaveEachHabitAtTheLastOne() {
        val queue = TickQueue(Memory())
        queue.add(1, "2026-11-26", at, done = true)
        queue.add(1, "2026-11-26", at.plusSeconds(1), done = false)
        queue.add(2, "2026-11-26", at, done = true)
        queue.add(3, "2026-11-25", at, done = true)
        assertEquals(mapOf(1L to false, 2L to true), TickQueue.doneByQueue(queue.entries(), "2026-11-26"))
        assertEquals(mapOf("2026-11-25" to true), TickQueue.doneByDay(queue.entries(), 3))
    }
}
