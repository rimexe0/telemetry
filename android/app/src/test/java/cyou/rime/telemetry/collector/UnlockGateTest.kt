package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class UnlockGateTest {
    private val now = Instant.parse("2026-11-14T10:00:00Z")

    @Test
    fun withoutPasswordNothingCanBeTurnedOff() {
        assertEquals(UnlockGate.Step.NO_PASSWORD, UnlockGate.step(now, armed = false, waitingUntil = null))
        assertEquals(UnlockGate.Step.NO_PASSWORD, UnlockGate.step(now, armed = false, waitingUntil = now.plusSeconds(600)))
    }

    @Test
    fun theFirstTryOnlyStartsTheWait() {
        assertEquals(UnlockGate.Step.START, UnlockGate.step(now, armed = true, waitingUntil = null))
        assertEquals(30L, UnlockGate.WAIT.toMinutes())
    }

    @Test
    fun duringTheWaitThePasswordIsNotAsked() {
        val until = now.plus(UnlockGate.WAIT)
        assertEquals(UnlockGate.Step.WAIT, UnlockGate.step(now, armed = true, waitingUntil = until))
        assertEquals(UnlockGate.Step.WAIT, UnlockGate.step(until.minusSeconds(1), armed = true, waitingUntil = until))
    }

    @Test
    fun afterTheWaitThePasswordIsAsked() {
        assertEquals(UnlockGate.Step.PASSWORD, UnlockGate.step(now, armed = true, waitingUntil = now.minusSeconds(1)))
    }

    @Test
    fun theSamePasswordGivesTheSameHashForTheSameSalt() {
        val salt = ByteArray(16) { it.toByte() }
        val first = UnlockGate.hash("correct horse battery", salt, 1_000)
        val second = UnlockGate.hash("correct horse battery", salt, 1_000)
        assertTrue(first.contentEquals(second))
        assertFalse(first.contentEquals(UnlockGate.hash("wrong horse battery", salt, 1_000)))
        assertEquals(32, first.size)
    }

    @Test
    fun aDifferentSaltGivesADifferentHash() {
        val first = UnlockGate.hash("correct horse battery", ByteArray(16) { 1 }, 1_000)
        val second = UnlockGate.hash("correct horse battery", ByteArray(16) { 2 }, 1_000)
        assertFalse(first.contentEquals(second))
    }
}
