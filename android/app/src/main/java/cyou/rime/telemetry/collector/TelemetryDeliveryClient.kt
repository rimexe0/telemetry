package cyou.rime.telemetry.collector

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

class DeliveryException(
    val status: Int,
    message: String,
) : IllegalStateException(message)

/** Sends one bounded, retry-safe batch and retains rows until a 202 receipt. */
class TelemetryDeliveryClient(
    private val queue: HealthQueue,
    private val deviceId: String,
    private val serverUrl: String,
    private val token: String,
) {
    fun drainOnce(limit: Int = 100): DeliveryResult {
        val rows = queue.pending(limit)
        if (rows.isEmpty()) return DeliveryResult(0, 0, null)
        require(rows.all { it.deviceId == deviceId }) { "pending rows belong to another device" }
        val itemIds = rows.map(::queueItemId).sorted()
        val batchId = sha256(itemIds.joinToString("\u0000"))
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
                            .putNullable("ended_at", row.endedAt)
                            .put("collected_at", row.collectedAt)
                            .put("source_schema_version", row.sourceSchemaVersion)
                            .put("original_payload", JSONObject(row.payloadJson)),
                    )
                }
            })
        val connection = URL(serverUrl.trimEnd('/') + "/v1/ingest/batches").openConnection() as HttpURLConnection
        connection.requestMethod = "POST"
        connection.connectTimeout = 15_000
        connection.readTimeout = 30_000
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer $token")
        connection.setRequestProperty("X-Telemetry-Device-ID", deviceId)
        connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
        val responseCode = connection.responseCode
        if (responseCode != HttpURLConnection.HTTP_ACCEPTED) {
            connection.errorStream?.close()
            throw DeliveryException(responseCode, "Telemetry ingest returned HTTP $responseCode")
        }
        connection.inputStream?.close()
        val acknowledged = queue.markDelivered(rows.map { it.rowId }, Instant.now().toString())
        return DeliveryResult(rows.size, acknowledged, batchId)
    }

    private fun queueItemId(row: QueuedObservation): String = sha256(
        listOf(row.sourceInstanceKey, row.sourceRecordKey, row.revisionDigest).joinToString("\u0000"),
    )

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )

    private fun sha256(value: String): String = MessageDigest.getInstance("SHA-256")
        .digest(value.toByteArray(Charsets.UTF_8))
        .joinToString("") { byte -> "%02x".format(byte) }
}
