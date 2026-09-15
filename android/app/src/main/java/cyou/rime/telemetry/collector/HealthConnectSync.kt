package cyou.rime.telemetry.collector

import android.content.Context
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.HealthConnectFeatures
import androidx.health.connect.client.changes.DeletionChange
import androidx.health.connect.client.changes.UpsertionChange
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.Record
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.health.connect.client.request.ChangesTokenRequest
import kotlinx.coroutines.CancellationException
import java.time.Duration
import java.time.Instant
import org.json.JSONObject

/**
 * Result of one Health Connect sync attempt.
 *
 * `states` always contains one entry for each supported type. This makes an
 * empty Health Connect datastore, a missing grant, an unsupported provider and
 * a failed read distinguishable to the caller. In particular, an empty result
 * is not treated as a successful Samsung Health import.
 */
data class HealthSyncResult(
    val recordsRead: Int,
    val insertedRevisions: Int,
    val states: Map<String, String>,
)

/**
 * Synchronizes approved Health Connect types into the app-private queue.
 *
 * Health Connect changes tokens are kept independently for each type. A token
 * is created before the initial/recovery read, and is advanced only after the
 * corresponding page has been durably enqueued. This avoids rereading a large
 * window on every scheduled run while retaining a safe bounded recovery path
 * when Health Connect expires a token.
 */
class HealthConnectSync(
    private val context: Context,
    private val queue: HealthQueue,
    private val deviceId: String,
) {
    suspend fun collect(background: Boolean): HealthSyncResult {
        val unavailable = { state: String ->
            HealthSyncResult(0, 0, supportedTypes.associateWith { state })
        }

        val sdkStatus = try {
            HealthConnectClient.getSdkStatus(context, HEALTH_CONNECT_PACKAGE)
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            return unavailable(HealthSyncState.ERROR)
        }
        if (sdkStatus != HealthConnectClient.SDK_AVAILABLE) {
            return unavailable(HealthSyncState.UNAVAILABLE)
        }

        val client = try {
            HealthConnectClient.getOrCreate(context, HEALTH_CONNECT_PACKAGE)
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            return unavailable(HealthSyncState.ERROR)
        }

        val granted = try {
            client.permissionController.getGrantedPermissions()
        } catch (error: CancellationException) {
            throw error
        } catch (_: Exception) {
            return unavailable(HealthSyncState.ERROR)
        }

        val backgroundPermissionGranted = if (background) {
            val featureStatus = try {
                client.features.getFeatureStatus(
                    HealthConnectFeatures.FEATURE_READ_HEALTH_DATA_IN_BACKGROUND,
                )
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                return unavailable(HealthSyncState.ERROR)
            }
            if (featureStatus != HealthConnectFeatures.FEATURE_STATUS_AVAILABLE) {
                return unavailable(HealthSyncState.UNAVAILABLE)
            }
            HealthPermission.PERMISSION_READ_HEALTH_DATA_IN_BACKGROUND in granted
        } else {
            true
        }

        val collector = HealthConnectCollector(client, queue, deviceId)
        val states = linkedMapOf<String, String>()
        var recordsRead = 0
        var insertedRevisions = 0

        supportedTypes.forEach { type ->
            val readPermission = readPermissions.getValue(type)
            if (readPermission !in granted) {
                states[type] = HealthSyncState.PERMISSION_REQUIRED
                return@forEach
            }
            if (!backgroundPermissionGranted) {
                states[type] = HealthSyncState.BACKGROUND_PERMISSION_REQUIRED
                return@forEach
            }

            val result = try {
                syncType(client, collector, type)
            } catch (error: CancellationException) {
                throw error
            } catch (_: SecurityException) {
                TypeSyncResult(0, 0, HealthSyncState.PERMISSION_REQUIRED)
            } catch (_: Exception) {
                // A provider error must not prevent device, usage or the other
                // Health Connect types from being collected by the worker.
                TypeSyncResult(0, 0, HealthSyncState.ERROR)
            }
            recordsRead += result.recordsRead
            insertedRevisions += result.insertedRevisions
            states[type] = result.state
        }

        val statusPreferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
        supportedTypes.forEach { type ->
            states["${type}_availability"] = statusPreferences.getString("known_data_$type", "unknown") ?: "unknown"
            if (statusPreferences.contains(gapKey(type))) {
                states["${type}_history_gap"] = "expired_cursor_only_30_days_reconciled"
            }
        }
        return HealthSyncResult(recordsRead, insertedRevisions, states)
    }

    private suspend fun syncType(
        client: HealthConnectClient,
        collector: HealthConnectCollector,
        type: String,
    ): TypeSyncResult {
        val preferences = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
        val storedToken = preferences.getString(tokenKey(type), null)
        return if (storedToken.isNullOrEmpty()) {
            // Reserve the token before the backfill. Changes made while the
            // backfill is running remain visible from this reserved point.
            val token = client.getChangesToken(
                ChangesTokenRequest(recordTypes = setOf(recordClasses.getValue(type))),
            )
            val now = Instant.now()
            val result = collector.collectType(type, now.minus(INITIAL_LOOKBACK), now)
            if (result.permissionRevoked) {
                TypeSyncResult(result.recordsRead, result.insertedRevisions, HealthSyncState.PERMISSION_REQUIRED)
            } else {
                val checkpointed = preferences.edit().putString(tokenKey(type), token)
                    .putString("known_data_$type", if (result.recordsRead == 0) "empty_initial_window" else "records_observed")
                    .commit()
                check(checkpointed) { "unable to persist Health Connect $type changes cursor" }
                TypeSyncResult(
                    result.recordsRead,
                    result.insertedRevisions,
                    HealthSyncState.initialReadState(result.recordsRead),
                )
            }
        } else {
            syncChanges(client, collector, type, storedToken)
        }
    }

    private suspend fun syncChanges(
        client: HealthConnectClient,
        collector: HealthConnectCollector,
        type: String,
        token: String,
    ): TypeSyncResult {
        var nextToken = token
        var recordsRead = 0
        var insertedRevisions = 0
        var pagesRead = 0
        var hasMore = false
        do {
            val response = client.getChanges(nextToken)
            if (response.changesTokenExpired) {
                return reconcileExpiredToken(client, collector, type)
            }

            val observations = response.changes.mapNotNull { change ->
                when (change) {
                    is UpsertionChange -> {
                        // This app does not write health records, but keep the
                        // guard so a future write path cannot re-import itself.
                        if (change.record.metadata.dataOrigin.packageName == context.packageName) {
                            null
                        } else {
                            collector.observationFor(type, change.record)
                        }
                    }
                    is DeletionChange -> deletionObservation(type, change.recordId)
                    else -> null
                }
            }
            recordsRead += response.changes.size
            insertedRevisions += queue.enqueue(observations).insertedRevisions
            if (response.changes.any { it is UpsertionChange }) {
                context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE).edit()
                    .putString("known_data_$type", "records_observed").commit()
            }

            // The queue write is complete before the cursor is advanced. A
            // crash before this line safely replays the page through the
            // queue's idempotent source-record/revision key.
            nextToken = response.nextChangesToken
            val checkpointed = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
                .edit()
                .putString(tokenKey(type), nextToken)
                .commit()
            check(checkpointed) { "unable to persist Health Connect $type changes cursor" }
            pagesRead++
            hasMore = response.hasMore
        } while (hasMore && pagesRead < MAX_CHANGE_PAGES_PER_RUN)

        return TypeSyncResult(
            recordsRead,
            insertedRevisions,
            HealthSyncState.changePollState(recordsRead, hasMore),
        )
    }

    private suspend fun reconcileExpiredToken(
        client: HealthConnectClient,
        collector: HealthConnectCollector,
        type: String,
    ): TypeSyncResult {
        // Reserve a fresh cursor before the bounded recovery read. If the
        // provider fails during the read, the old cursor remains in storage and
        // the next run retries recovery instead of silently advancing a gap.
        val freshToken = client.getChangesToken(
            ChangesTokenRequest(recordTypes = setOf(recordClasses.getValue(type))),
        )
        val now = Instant.now()
        val result = collector.collectType(type, now.minus(RECOVERY_LOOKBACK), now)
        if (result.permissionRevoked) {
            return TypeSyncResult(result.recordsRead, result.insertedRevisions, HealthSyncState.PERMISSION_REQUIRED)
        }
        val checkpointed = context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE)
            .edit()
            .putString(tokenKey(type), freshToken)
            .putString("known_data_$type", if (result.recordsRead == 0) "empty_recovery_window" else "records_observed")
            .putString(gapKey(type), "expired_changes_token:${now.toString()}")
            .commit()
        check(checkpointed) { "unable to persist Health Connect $type recovery cursor" }
        return TypeSyncResult(
            result.recordsRead,
            result.insertedRevisions,
            HealthSyncState.GAP_RECONCILED,
        )
    }

    private fun deletionObservation(type: String, recordId: String): HealthObservation {
        val collectedAt = Instant.now().toString()
        return HealthObservation(
            sourceInstanceKey = "health_connect:$deviceId",
            sourceKind = "health_connect",
            deviceId = deviceId,
            sourceRecordKey = "$type:$recordId",
            kind = "health_connect.$type.deleted",
            recordType = type,
            occurredAt = collectedAt,
            endedAt = null,
            collectedAt = collectedAt,
            sourceSchemaVersion = "health-connect-client-1.1.0",
            payloadJson = JSONObject()
                .put("record_type", type)
                .put("deleted_record_id", recordId)
                .put("deleted", true)
                .toString(),
        )
    }

    internal object HealthSyncState {
        const val OK = "ok"
        const val EMPTY = "empty"
        const val UNCHANGED = "unchanged"
        const val PARTIAL = "partial"
        const val RECONCILED = "reconciled"
        const val GAP_RECONCILED = "gap_reconciled"
        const val PERMISSION_REQUIRED = "permission_required"
        const val BACKGROUND_PERMISSION_REQUIRED = "background_permission_required"
        const val UNAVAILABLE = "unavailable"
        const val ERROR = "error"

        fun forRecordCount(recordsRead: Int): String = if (recordsRead == 0) EMPTY else OK

        fun initialReadState(recordsRead: Int): String = if (recordsRead == 0) EMPTY else RECONCILED

        fun changePollState(recordsRead: Int, hasMore: Boolean): String = when {
            hasMore -> PARTIAL
            recordsRead == 0 -> UNCHANGED
            else -> OK
        }
    }

    private data class TypeSyncResult(
        val recordsRead: Int,
        val insertedRevisions: Int,
        val state: String,
    )

    private fun tokenKey(type: String): String = "changes_token_$type"

    private fun gapKey(type: String): String = "changes_gap_$type"

    private companion object {
        const val HEALTH_CONNECT_PACKAGE = "com.google.android.apps.healthdata"
        const val PREFERENCES_NAME = "telemetry-health-sync"
        const val MAX_CHANGE_PAGES_PER_RUN = 4
        val INITIAL_LOOKBACK: Duration = Duration.ofDays(30)
        val RECOVERY_LOOKBACK: Duration = Duration.ofDays(30)
        val supportedTypes = listOf("steps", "heart_rate", "sleep_session", "exercise_session")
        val recordClasses: Map<String, kotlin.reflect.KClass<out Record>> = mapOf(
            "steps" to StepsRecord::class,
            "heart_rate" to HeartRateRecord::class,
            "sleep_session" to SleepSessionRecord::class,
            "exercise_session" to ExerciseSessionRecord::class,
        )
        val readPermissions = mapOf(
            "steps" to HealthPermission.getReadPermission(StepsRecord::class),
            "heart_rate" to HealthPermission.getReadPermission(HeartRateRecord::class),
            "sleep_session" to HealthPermission.getReadPermission(SleepSessionRecord::class),
            "exercise_session" to HealthPermission.getReadPermission(ExerciseSessionRecord::class),
        )
    }
}
