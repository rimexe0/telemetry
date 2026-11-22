package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class DeliveryBatchPlannerTest {
    @Test
    fun byteBudgetIsConsumedAcrossSequentialBatches() {
        val rows = (0..500).toList()
        val totalBudget = 100_000L
        val bodyBuilder: (List<Int>) -> ByteArray = { values -> ByteArray(values.size * 100) }

        val first = DeliveryBatchPlanner.plan(rows.take(500), totalBudget, bodyBuilder)!!
        val usedAfterFirst = first.body.size.toLong()
        val remaining = DeliveryBatchBudget.remaining(totalBudget, usedAfterFirst)
        val second = DeliveryBatchPlanner.plan(rows.drop(500), remaining, bodyBuilder)!!
        val totalUsed = usedAfterFirst + second.body.size.toLong()

        assertEquals(500, first.rows.size)
        assertEquals(1, second.rows.size)
        assertEquals(50_100L, totalUsed)
        assertTrue(totalUsed <= totalBudget)
    }

    @Test
    fun remainingBudgetDoesNotResetForNextBatch() {
        val rows = (0..500).toList()
        val totalBudget = 50_050L
        val bodyBuilder: (List<Int>) -> ByteArray = { values -> ByteArray(values.size * 100) }
        val first = DeliveryBatchPlanner.plan(rows.take(500), totalBudget, bodyBuilder)!!
        val remaining = DeliveryBatchBudget.remaining(totalBudget, first.body.size.toLong())

        try {
            DeliveryBatchPlanner.plan(rows.drop(500), remaining, bodyBuilder)
            throw AssertionError("the second batch must not reset the total byte budget")
        } catch (error: OversizedDeliveryRowException) {
            assertEquals(100L, error.bodyBytes)
            assertEquals(50L, remaining)
        }
    }

    @Test
    fun aRequestStaysSmallWhileTheDrainBudgetIsLarger() {
        val rows = (0 until 500).toList()
        val bodyBuilder: (List<Int>) -> ByteArray = { values -> ByteArray(values.size * 1_000) }
        val request = DeliveryBatchPlanner.planRequest(rows, 262_144L, 1_048_576L, bodyBuilder)!!
        assertEquals(262, request.rows.size)
    }

    @Test
    fun aRowLargerThanARequestStillGoesAloneWithinTheDrainBudget() {
        val rows = listOf(300_000, 10)
        val bodyBuilder: (List<Int>) -> ByteArray = { values -> ByteArray(values.sum()) }
        val request = DeliveryBatchPlanner.planRequest(rows, 262_144L, 1_048_576L, bodyBuilder)!!
        assertEquals(listOf(300_000), request.rows)
        try {
            DeliveryBatchPlanner.planRequest(listOf(2_000_000), 262_144L, 1_048_576L, bodyBuilder)
            throw AssertionError("a row over the drain budget must still be refused")
        } catch (error: OversizedDeliveryRowException) {
            assertEquals(2_000_000L, error.bodyBytes)
        }
    }

    @Test
    fun aCycleThatGotRowsThroughDoesNotWaitLonger() {
        assertEquals(DeliveryBackoff.Next(0, 0L), DeliveryBackoff.after(5, madeProgress = true))
        assertEquals(DeliveryBackoff.Next(1, 60_000L), DeliveryBackoff.after(0, madeProgress = false))
        assertEquals(DeliveryBackoff.Next(3, 240_000L), DeliveryBackoff.after(2, madeProgress = false))
        assertEquals(DeliveryBackoff.Next(5, 900_000L), DeliveryBackoff.after(5, madeProgress = false))
    }
}
