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

/**
 * Plans one request of at most [requestBytes] within the drain's [remainingBytes].
 * Small requests finish on a slow link; a single row larger than a request
 * still goes alone while the drain's budget allows it (2026-11-22: 1 MiB
 * requests timed out on the phone for seven hours).
 */
internal fun <T> DeliveryBatchPlanner.planRequest(
    rows: List<T>,
    requestBytes: Long,
    remainingBytes: Long,
    buildBody: (List<T>) -> ByteArray,
): PlannedDeliveryBatch<T>? = try {
    plan(rows, minOf(requestBytes, remainingBytes), buildBody)
} catch (error: OversizedDeliveryRowException) {
    if (requestBytes >= remainingBytes) throw error
    plan(rows.take(1), remainingBytes, buildBody)
}

/**
 * The next retry after a failed delivery cycle. A cycle that got rows through
 * before failing is progress, not a failure: it tries again at the next cycle
 * and does not lengthen the wait. Otherwise the wait doubles from one minute
 * up to fifteen.
 */
internal object DeliveryBackoff {
    data class Next(val failures: Int, val waitMillis: Long)

    fun after(previousFailures: Int, madeProgress: Boolean): Next {
        if (madeProgress) return Next(0, 0L)
        val failures = (previousFailures + 1).coerceAtMost(5)
        return Next(failures, minOf(900_000L, 60_000L shl (failures - 1)))
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
