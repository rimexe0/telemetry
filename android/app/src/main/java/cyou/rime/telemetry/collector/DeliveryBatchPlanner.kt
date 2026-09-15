package cyou.rime.telemetry.collector

/** A payload and the queue rows that were used to build it. */
internal data class PlannedDeliveryBatch<T>(
    val rows: List<T>,
    val body: ByteArray,
)

/**
 * Selects the longest FIFO prefix whose encoded request fits the byte budget.
 * The body builder is called with the exact rows that will be sent, so the
 * caller can include envelope fields such as the stable batch ID when sizing.
 */
internal object DeliveryBatchPlanner {
    fun <T> plan(
        rows: List<T>,
        maxBytes: Long,
        buildBody: (List<T>) -> ByteArray,
    ): PlannedDeliveryBatch<T>? {
        require(maxBytes > 0) { "maxBytes must be positive" }
        if (rows.isEmpty()) return null

        val firstRows = rows.subList(0, 1)
        val firstBody = buildBody(firstRows)
        if (firstBody.size.toLong() > maxBytes) {
            throw OversizedDeliveryRowException(firstBody.size.toLong())
        }

        // The body grows monotonically with a FIFO prefix. Binary search keeps
        // a large backlog from repeatedly serializing every intermediate row.
        var low = if (rows.size > 1) 2 else 1
        var high = rows.size
        var bestRows = firstRows
        var bestBody = firstBody
        while (low <= high) {
            val middle = low + (high - low) / 2
            val candidateRows = rows.subList(0, middle)
            val candidateBody = buildBody(candidateRows)
            if (candidateBody.size.toLong() <= maxBytes) {
                bestRows = candidateRows
                bestBody = candidateBody
                low = middle + 1
            } else {
                high = middle - 1
            }
        }
        return PlannedDeliveryBatch(bestRows, bestBody)
    }
}

/** Keeps the byte cap an aggregate drain budget rather than a per-batch cap. */
internal object DeliveryBatchBudget {
    fun remaining(maxBytes: Long, usedBytes: Long): Long {
        require(maxBytes > 0) { "maxBytes must be positive" }
        require(usedBytes in 0..maxBytes) { "usedBytes must be within maxBytes" }
        return maxBytes - usedBytes
    }
}

/** The FIFO head cannot fit even when sent as a one-row request. */
internal class OversizedDeliveryRowException(
    bodyBytes: Long,
    partialResult: DeliveryDrainResult? = null,
) : DeliveryException(
    status = 0,
    message = "one queued observation requires $bodyBytes bytes, over the delivery limit",
    kind = DeliveryFailureKind.OVERSIZED_ROW,
    partialResult = partialResult,
    bodyBytes = bodyBytes,
)
