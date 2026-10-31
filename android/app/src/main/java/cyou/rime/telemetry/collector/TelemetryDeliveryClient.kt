package cyou.rime.telemetry.collector

import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.time.Instant
import org.json.JSONArray
import org.json.JSONObject

data class DeliveryResult(
    val claimed: Int,
    val acknowledged: Int,
    val batchId: String?,
)

data class DeliveryDrainResult(
    val claimed: Int,
    val acknowledged: Int,
    val batches: Int,
    val bodyBytes: Long,
    val hasMore: Boolean,
)

enum class DeliveryFailureKind {
    HTTP_REJECTED,
    NETWORK,
    OVERSIZED_ROW,
    INVALID_PAYLOAD,
    INVALID_RECEIPT,
    LOCAL_ACKNOWLEDGEMENT,
}

open class DeliveryException(
    val status: Int,
    message: String,
    val kind: DeliveryFailureKind? = null,
    val partialResult: DeliveryDrainResult? = null,
    /** The encoded body for the attempted request, when one existed. */
    val bodyBytes: Long = 0,
    cause: Throwable? = null,
) : IllegalStateException(message, cause)

/** Sends bounded, retry-safe batches and retains rows until a 202 receipt. */
class TelemetryDeliveryClient(
    private val queue: HealthQueue,
    private val deviceId: String,
    private val serverUrl: String,
    private val token: String,
    private val sealed: SealedChannel?,
) {
    /**
     * Keeps the existing one-batch API for the manual action and old worker.
     * It uses the old row limit and does not impose the bounded-drain byte cap.
     */
    fun drainOnce(limit: Int = 100): DeliveryResult = synchronized(DRAIN_LOCK) {
        val rows = queue.pending(limit)
        if (rows.isEmpty()) return@synchronized DeliveryResult(0, 0, null)
        require(rows.all { it.deviceId == deviceId }) { "pending rows belong to another device" }
        val payload = try {
            buildPayload(rows)
        } catch (error: Exception) {
            throw DeliveryException(
                status = 0,
                message = "could not encode telemetry batch",
                kind = DeliveryFailureKind.INVALID_PAYLOAD,
                cause = error,
            )
        }
        sendWithFailureMetrics(payload)
        val deliveredAt = Instant.now().toString()
        val acknowledged = try {
            queue.markDelivered(rows.map { it.rowId }, deliveredAt)
        } catch (error: Exception) {
            recordFailedAttempt(payload.body.size.toLong())
            throw DeliveryException(
                status = 0,
                message = "telemetry batch was accepted but local acknowledgement failed",
                kind = DeliveryFailureKind.LOCAL_ACKNOWLEDGEMENT,
                bodyBytes = payload.body.size.toLong(),
                cause = error,
            )
        }
        try {
            queue.recordDeliveryAttempt(deliveredAt, payload.body.size.toLong(), successful = true)
        } catch (error: Exception) {
            throw DeliveryException(
                status = 0,
                message = "telemetry batch was accepted but delivery metrics could not be recorded",
                kind = DeliveryFailureKind.LOCAL_ACKNOWLEDGEMENT,
                bodyBytes = payload.body.size.toLong(),
                cause = error,
            )
        }
        return@synchronized DeliveryResult(rows.size, acknowledged, payload.batchId)
    }

    /**
     * Drains at most the requested number of FIFO batches, byte budget and
     * elapsed time. It stops on the first failure and leaves that row pending.
     * Concurrent manual, worker or foreground drains in this process wait for
     * the same lock and then observe the queue state left by the first drain.
     */
    fun drainBounded(
        maxBatches: Int = DEFAULT_MAX_BATCHES,
        maxBytes: Long = DEFAULT_MAX_BYTES,
        maxMillis: Long = DEFAULT_MAX_MILLIS,
    ): DeliveryDrainResult = synchronized(DRAIN_LOCK) {
        require(maxBatches > 0) { "maxBatches must be positive" }
        require(maxBytes > 0) { "maxBytes must be positive" }
        require(maxMillis > 0) { "maxMillis must be positive" }

        val startedAt = System.nanoTime()
        val maxDurationNanos = maxMillis.coerceAtMost(
            ((Long.MAX_VALUE - startedAt).coerceAtLeast(1L)) / 1_000_000L,
        ) * 1_000_000L
        val deadline = startedAt + maxDurationNanos
        var result = DeliveryDrainResult(0, 0, 0, 0, hasMore = false)

        while (result.batches < maxBatches && System.nanoTime() < deadline) {
            if (result.bodyBytes >= maxBytes) {
                return@synchronized result.copy(hasMore = queue.pending(1).isNotEmpty())
            }
            val pending = queue.pending(MAX_ROWS_PER_BATCH)
            if (pending.isEmpty()) return@synchronized result.copy(hasMore = false)
            require(pending.all { it.deviceId == deviceId }) {
                "pending rows belong to another device"
            }

            val plan = try {
                DeliveryBatchPlanner.plan(
                    pending,
                    DeliveryBatchBudget.remaining(maxBytes, result.bodyBytes),
                    ::buildBody,
                )
            } catch (error: OversizedDeliveryRowException) {
                if (result.bodyBytes > 0) {
                    return@synchronized result.copy(hasMore = true)
                }
                throw OversizedDeliveryRowException(error.bodyBytes, result.copy(hasMore = true))
            } catch (error: Exception) {
                throw DeliveryException(
                    status = 0,
                    message = "could not encode telemetry batch",
                    kind = DeliveryFailureKind.INVALID_PAYLOAD,
                    partialResult = result.copy(hasMore = true),
                    cause = error,
                )
            } ?: return@synchronized result.copy(hasMore = false)

            if (System.nanoTime() >= deadline) {
                return@synchronized result.copy(hasMore = true)
            }

            val attempted = result.copy(
                claimed = result.claimed + plan.rows.size,
                batches = result.batches + 1,
                bodyBytes = result.bodyBytes + plan.body.size.toLong(),
                hasMore = true,
            )
            try {
                val remainingMillis = ((deadline - System.nanoTime()) / 1_000_000L).coerceAtLeast(1L)
                sendWithFailureMetrics(
                    DeliveryPayload(plan.rows.size, batchId(plan.rows), plan.body),
                    remainingMillis,
                )
            } catch (error: DeliveryException) {
                throw error.withPartial(attempted)
            } catch (error: Exception) {
                throw DeliveryException(
                    status = 0,
                    message = "telemetry batch delivery failed",
                    kind = DeliveryFailureKind.NETWORK,
                    partialResult = attempted,
                    bodyBytes = plan.body.size.toLong(),
                    cause = error,
                )
            }

            val deliveredAt = Instant.now().toString()
            val acknowledged = try {
                queue.markDelivered(plan.rows.map { it.rowId }, deliveredAt)
            } catch (error: Exception) {
                recordFailedAttempt(plan.body.size.toLong())
                throw DeliveryException(
                    status = 0,
                    message = "telemetry batch was accepted but local acknowledgement failed",
                    kind = DeliveryFailureKind.LOCAL_ACKNOWLEDGEMENT,
                    partialResult = attempted.copy(acknowledged = result.acknowledged),
                    bodyBytes = plan.body.size.toLong(),
                    cause = error,
                )
            }
            val afterAcknowledgement = attempted.copy(acknowledged = result.acknowledged + acknowledged)
            try {
                queue.recordDeliveryAttempt(deliveredAt, plan.body.size.toLong(), successful = true)
            } catch (error: Exception) {
                throw DeliveryException(
                    status = 0,
                    message = "telemetry batch was accepted but delivery metrics could not be recorded",
                    kind = DeliveryFailureKind.LOCAL_ACKNOWLEDGEMENT,
                    partialResult = afterAcknowledgement.copy(hasMore = true),
                    bodyBytes = plan.body.size.toLong(),
                    cause = error,
                )
            }
            result = afterAcknowledgement
            if (acknowledged != plan.rows.size) {
                throw DeliveryException(
                    status = 0,
                    message = "telemetry batch was accepted but only $acknowledged/${plan.rows.size} rows were acknowledged locally",
                    kind = DeliveryFailureKind.LOCAL_ACKNOWLEDGEMENT,
                    partialResult = result.copy(hasMore = true),
                    bodyBytes = plan.body.size.toLong(),
                )
            }
        }

        return@synchronized result.copy(hasMore = queue.pending(1).isNotEmpty())
    }

    private fun buildBody(rows: List<QueuedObservation>): ByteArray = buildPayload(rows).body

    private fun buildPayload(rows: List<QueuedObservation>): DeliveryPayload {
        val batchId = batchId(rows)
        val body = JSONObject()
            .put("id", batchId)
            .put("device_id", deviceId)
            .put("collector_version", "android-0.1")
            .put("observations", JSONArray().apply {
                rows.forEach { row ->
                    put(
                        JSONObject()
                            .put("source_instance_key", row.sourceInstanceKey)
                            .put("source_kind", row.sourceKind)
                            .put("device_id", row.deviceId)
                            .put("source_record_key", row.sourceRecordKey)
                            .put("kind", row.kind)
                            .put("occurred_at", row.occurredAt)
                            .put("ended_at", row.endedAt ?: JSONObject.NULL)
                            .put("collected_at", row.collectedAt)
                            .put("source_schema_version", row.sourceSchemaVersion)
                            .put("original_payload", JSONObject(row.payloadJson)),
                    )
                }
            })
        return DeliveryPayload(rows.size, batchId, body.toString().toByteArray(Charsets.UTF_8))
    }

    private fun batchId(rows: List<QueuedObservation>): String =
        sha256(rows.map(::queueItemId).sorted().joinToString("\u0000"))

    private fun send(payload: DeliveryPayload, timeoutMillis: Long? = null) {
        val body = payload.body
        val requestTimeoutMillis = timeoutMillis?.coerceIn(1L, 30_000L)
        val connectTimeout = requestTimeoutMillis?.coerceAtMost(15_000L)?.toInt() ?: 15_000
        val readTimeout = requestTimeoutMillis?.toInt() ?: 30_000
        // Through the sealed relay first; the direct address (Tailscale) only
        // when the relay cannot be reached. Ingest is retry-safe by batch id.
        sealed?.let { channel ->
            val answer = try {
                channel.exchange(
                    "POST",
                    "/v1/ingest/batches",
                    mapOf(
                        "Accept" to "application/json",
                        "Content-Type" to "application/json",
                        "Authorization" to "Bearer $token",
                        "X-Telemetry-Device-ID" to deviceId,
                    ),
                    body,
                    connectTimeout,
                    readTimeout,
                )
            } catch (_: java.io.IOException) {
                null
            }
            if (answer != null) {
                checkReceipt(payload, answer.status) { answer.body.inputStream() }
                return
            }
        }
        val connection = try {
            URL(serverUrl.trimEnd('/') + "/v1/ingest/batches")
                .openConnection() as HttpURLConnection
        } catch (error: Exception) {
            throw DeliveryException(
                status = 0,
                message = "Telemetry ingest request could not be opened",
                kind = DeliveryFailureKind.NETWORK,
                bodyBytes = body.size.toLong(),
                cause = error,
            )
        }
        try {
            connection.requestMethod = "POST"
            connection.connectTimeout = connectTimeout
            connection.readTimeout = readTimeout
            connection.doOutput = true
            connection.useCaches = false
            connection.instanceFollowRedirects = false
            connection.setFixedLengthStreamingMode(body.size)
            connection.setRequestProperty("Accept", "application/json")
            connection.setRequestProperty("Content-Type", "application/json")
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("X-Telemetry-Device-ID", deviceId)
            connection.outputStream.use { it.write(body) }
            val responseCode = connection.responseCode
            if (responseCode != HttpURLConnection.HTTP_ACCEPTED) {
                closeQuietly(connection.errorStream)
            }
            checkReceipt(payload, responseCode) { connection.inputStream }
        } catch (error: DeliveryException) {
            throw error
        } catch (error: Exception) {
            throw DeliveryException(
                status = 0,
                message = "Telemetry ingest request failed",
                kind = DeliveryFailureKind.NETWORK,
                bodyBytes = body.size.toLong(),
                cause = error,
            )
        } finally {
            connection.disconnect()
        }
    }

    /** Accepts only a 202 whose receipt names this batch and its row count. */
    private fun checkReceipt(payload: DeliveryPayload, responseCode: Int, receiptStream: () -> InputStream) {
        val bodyBytes = payload.body.size.toLong()
        if (responseCode != HttpURLConnection.HTTP_ACCEPTED) {
            throw DeliveryException(
                status = responseCode,
                message = "Telemetry ingest returned HTTP $responseCode",
                kind = DeliveryFailureKind.HTTP_REJECTED,
                bodyBytes = bodyBytes,
            )
        }
        val receipt = try {
            receiptStream().use(::readReceipt)
        } catch (error: Exception) {
            throw DeliveryException(
                status = 202,
                message = "Telemetry ingest returned an unreadable receipt",
                kind = DeliveryFailureKind.INVALID_RECEIPT,
                bodyBytes = bodyBytes,
                cause = error,
            )
        }
        if (receipt.optString("batch_id") != payload.batchId ||
            receipt.optInt("observations_seen", -1) != payload.observationCount
        ) {
            throw DeliveryException(
                status = 202,
                message = "Telemetry ingest returned an invalid receipt",
                kind = DeliveryFailureKind.INVALID_RECEIPT,
                bodyBytes = bodyBytes,
            )
        }
    }

    private fun sendWithFailureMetrics(payload: DeliveryPayload, timeoutMillis: Long? = null) {
        try {
            send(payload, timeoutMillis)
        } catch (error: Exception) {
            recordFailedAttempt(payload.body.size.toLong())
            throw error
        }
    }

    private fun recordFailedAttempt(bodyBytes: Long) {
        runCatching {
            queue.recordDeliveryAttempt(Instant.now().toString(), bodyBytes, successful = false)
        }
    }

    private fun DeliveryException.withPartial(partial: DeliveryDrainResult): DeliveryException =
        DeliveryException(
            status = status,
            message = message ?: "telemetry delivery failed",
            kind = kind,
            partialResult = partial,
            bodyBytes = bodyBytes,
            cause = this,
        )

    private fun queueItemId(row: QueuedObservation): String = sha256(
        listOf(row.sourceInstanceKey, row.sourceRecordKey, row.revisionDigest).joinToString("\u0000"),
    )

    private fun closeQuietly(stream: InputStream?) {
        runCatching { stream?.close() }
    }

    private fun readReceipt(stream: InputStream): JSONObject {
        val bytes = ByteArray(MAX_RECEIPT_BYTES + 1)
        var count = 0
        while (count < bytes.size) {
            val read = stream.read(bytes, count, bytes.size - count)
            if (read < 0) break
            count += read
        }
        if (count > MAX_RECEIPT_BYTES) {
            throw IllegalStateException("Telemetry ingest receipt exceeds $MAX_RECEIPT_BYTES bytes")
        }
        return JSONObject(String(bytes, 0, count, Charsets.UTF_8))
    }

    private data class DeliveryPayload(
        val observationCount: Int,
        val batchId: String,
        val body: ByteArray,
    )

    private companion object {
        const val MAX_ROWS_PER_BATCH = 500
        const val DEFAULT_MAX_BATCHES = 10
        const val DEFAULT_MAX_BYTES = 1_048_576L
        const val DEFAULT_MAX_MILLIS = 20_000L
        const val MAX_RECEIPT_BYTES = 64 * 1024
        val DRAIN_LOCK = Any()

        fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(Charsets.UTF_8))
            .joinToString("") { byte -> "%02x".format(byte) }
    }
}
