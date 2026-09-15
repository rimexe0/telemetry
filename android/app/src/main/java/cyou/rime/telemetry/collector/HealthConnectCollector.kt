package cyou.rime.telemetry.collector

import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.Record
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.health.connect.client.records.metadata.Metadata
import androidx.health.connect.client.request.ReadRecordsRequest
import androidx.health.connect.client.time.TimeRangeFilter
import java.time.Instant
import kotlin.reflect.KClass
import org.json.JSONArray
import org.json.JSONObject

data class HealthCollectionResult(
    val recordsRead: Int,
    val insertedRevisions: Int,
    val recordsReadByType: Map<String, Int>,
    val insertedByType: Map<String, Int>,
    val queueStats: HealthQueueStats,
)

internal data class HealthTypeCollectionResult(
    val recordsRead: Int,
    val insertedRevisions: Int,
    /** True when Health Connect revoked this type while it was being read. */
    val permissionRevoked: Boolean = false,
)

/** Reads approved Health Connect types page-by-page into the local queue. */
class HealthConnectCollector(
    private val client: HealthConnectClient,
    private val queue: HealthQueue,
    private val deviceId: String,
) {
    suspend fun collectAll(
        start: Instant = Instant.EPOCH,
        end: Instant = Instant.now(),
    ): HealthCollectionResult {
        val resultsByType = linkedMapOf(
            "steps" to collectType("steps", start, end),
            "heart_rate" to collectType("heart_rate", start, end),
            "sleep_session" to collectType("sleep_session", start, end),
            "exercise_session" to collectType("exercise_session", start, end),
        )
        return HealthCollectionResult(
            recordsRead = resultsByType.values.sumOf { it.recordsRead },
            insertedRevisions = resultsByType.values.sumOf { it.insertedRevisions },
            recordsReadByType = resultsByType.mapValues { it.value.recordsRead },
            insertedByType = resultsByType.mapValues { it.value.insertedRevisions },
            queueStats = queue.stats(),
        )
    }

    /**
     * Collect one approved type over a bounded range. The sync wrapper uses
     * this to isolate a revoked or failing type from the other types.
     */
    internal suspend fun collectType(
        recordType: String,
        start: Instant,
        end: Instant,
    ): HealthTypeCollectionResult = when (recordType) {
        "steps" -> collectRecords(StepsRecord::class, "steps", ::stepsObservation, start, end)
        "heart_rate" -> collectRecords(HeartRateRecord::class, "heart_rate", ::heartRateObservation, start, end)
        "sleep_session" -> collectRecords(SleepSessionRecord::class, "sleep_session", ::sleepObservation, start, end)
        "exercise_session" -> collectRecords(ExerciseSessionRecord::class, "exercise_session", ::exerciseObservation, start, end)
        else -> throw IllegalArgumentException("unsupported Health Connect record type: $recordType")
    }

    /** Converts a change-log upsertion using the same schema as a range read. */
    internal fun observationFor(recordType: String, record: Record): HealthObservation = when (recordType) {
        "steps" -> (record as? StepsRecord)?.let(::stepsObservation)
        "heart_rate" -> (record as? HeartRateRecord)?.let(::heartRateObservation)
        "sleep_session" -> (record as? SleepSessionRecord)?.let(::sleepObservation)
        "exercise_session" -> (record as? ExerciseSessionRecord)?.let(::exerciseObservation)
        else -> null
    } ?: throw IllegalArgumentException("Health Connect change has unexpected type for $recordType")

    private suspend fun <T : Record> collectRecords(
        recordClass: KClass<T>,
        recordType: String,
        toObservation: (T) -> HealthObservation,
        start: Instant,
        end: Instant,
    ): HealthTypeCollectionResult {
        var pageToken: String? = null
        var recordsRead = 0
        var inserted = 0
        do {
            val response = try {
                client.readRecords(
                    ReadRecordsRequest(
                        recordType = recordClass,
                        timeRangeFilter = TimeRangeFilter.between(start, end),
                        ascendingOrder = true,
                        pageSize = 500,
                        pageToken = pageToken,
                    ),
                )
            } catch (_: SecurityException) {
                // A source may have been revoked independently. Keep the other
                // approved types collecting and let the next run retry this one.
                return HealthTypeCollectionResult(recordsRead, inserted, permissionRevoked = true)
            }
            recordsRead += response.records.size
            inserted += queue.enqueue(response.records.map(toObservation)).insertedRevisions
            pageToken = response.pageToken
        } while (!pageToken.isNullOrEmpty())
        return HealthTypeCollectionResult(recordsRead, inserted)
    }

    private fun stepsObservation(record: StepsRecord): HealthObservation {
        val payload = intervalPayload("steps", record.metadata, record.startTime, record.endTime)
            .put("count", record.count)
            .putNullable("start_zone_offset", record.startZoneOffset)
            .putNullable("end_zone_offset", record.endZoneOffset)
        return observation("steps", record.metadata, record.startTime, record.endTime, payload)
    }

    private fun heartRateObservation(record: HeartRateRecord): HealthObservation {
        val payload = intervalPayload("heart_rate", record.metadata, record.startTime, record.endTime)
            .putNullable("start_zone_offset", record.startZoneOffset)
            .putNullable("end_zone_offset", record.endZoneOffset)
            .put(
                "samples",
                JSONArray().apply {
                    record.samples.forEach { sample ->
                        put(
                            JSONObject()
                                .put("time", sample.time.toString())
                                .put("beats_per_minute", sample.beatsPerMinute),
                        )
                    }
                },
            )
        return observation("heart_rate", record.metadata, record.startTime, record.endTime, payload)
    }

    private fun sleepObservation(record: SleepSessionRecord): HealthObservation {
        val payload = intervalPayload("sleep_session", record.metadata, record.startTime, record.endTime)
            .putNullable("start_zone_offset", record.startZoneOffset)
            .putNullable("end_zone_offset", record.endZoneOffset)
            .putNullable("title", record.title)
            .putNullable("notes", record.notes)
            .put(
                "stages",
                JSONArray().apply {
                    record.stages.forEach { stage ->
                        put(
                            JSONObject()
                                .put("start_time", stage.startTime.toString())
                                .put("end_time", stage.endTime.toString())
                                .put("stage", stage.stage),
                        )
                    }
                },
            )
        return observation("sleep_session", record.metadata, record.startTime, record.endTime, payload)
    }

    private fun exerciseObservation(record: ExerciseSessionRecord): HealthObservation {
        val payload = intervalPayload("exercise_session", record.metadata, record.startTime, record.endTime)
            .putNullable("start_zone_offset", record.startZoneOffset)
            .putNullable("end_zone_offset", record.endZoneOffset)
            .put("exercise_type", record.exerciseType)
            .putNullable("title", record.title)
            .putNullable("notes", record.notes)
            .putNullable("planned_exercise_session_id", record.plannedExerciseSessionId)
            .put(
                "segments",
                JSONArray().apply {
                    record.segments.forEach { segment ->
                        put(
                            JSONObject()
                                .put("start_time", segment.startTime.toString())
                                .put("end_time", segment.endTime.toString())
                                .put("segment_type", segment.segmentType)
                                .put("repetitions", segment.repetitions),
                        )
                    }
                },
            )
            .put(
                "laps",
                JSONArray().apply {
                    record.laps.forEach { lap ->
                        put(
                            JSONObject()
                                .put("start_time", lap.startTime.toString())
                                .put("end_time", lap.endTime.toString())
                                .put("length", lap.length.toString()),
                        )
                    }
                },
            )
            .put("unread_fields", JSONArray().put("exercise_route"))
        return observation("exercise_session", record.metadata, record.startTime, record.endTime, payload)
    }

    private fun intervalPayload(
        recordType: String,
        metadata: Metadata,
        start: Instant,
        end: Instant,
    ): JSONObject = JSONObject()
        .put("record_type", recordType)
        .put("metadata", metadataPayload(metadata))
        .put("start_time", start.toString())
        .put("end_time", end.toString())

    private fun observation(
        recordType: String,
        metadata: Metadata,
        start: Instant,
        end: Instant,
        payload: JSONObject,
    ): HealthObservation = HealthObservation(
        sourceInstanceKey = "health_connect:$deviceId",
        sourceKind = "health_connect",
        deviceId = deviceId,
        sourceRecordKey = "$recordType:${metadata.id}",
        kind = "health_connect.$recordType",
        recordType = recordType,
        occurredAt = start.toString(),
        endedAt = end.toString(),
        collectedAt = Instant.now().toString(),
        sourceSchemaVersion = "health-connect-client-1.1.0",
        payloadJson = payload.toString(),
    )

    private fun metadataPayload(metadata: Metadata): JSONObject = JSONObject()
        .put("id", metadata.id)
        .put("data_origin", metadata.dataOrigin.packageName)
        .put("last_modified_time", metadata.lastModifiedTime.toString())
        .putNullable("client_record_id", metadata.clientRecordId)
        .put("client_record_version", metadata.clientRecordVersion)
        .put("recording_method", metadata.recordingMethod)
            .putNullable(
                "device",
                metadata.device?.let { device ->
                    JSONObject()
                        .put("type", device.type)
                        .put("manufacturer", device.manufacturer)
                        .put("model", device.model)
                },
            )

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )
}
