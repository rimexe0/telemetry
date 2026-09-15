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
}
