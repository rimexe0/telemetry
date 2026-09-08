package cyou.rime.telemetry.collector

import android.content.Context
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import java.time.Duration
import java.time.Instant

/**
 * Collects recent source data into the local queue without requiring a network.
 * The queue's source identity and revision digest make each overlapping run
 * safe to repeat after a crash or a delayed WorkManager retry.
 */
class TelemetryCollectionWorker(
    context: Context,
    workerParameters: WorkerParameters,
) : CoroutineWorker(context, workerParameters) {
    override suspend fun doWork(): Result {
        val queue = HealthQueue(applicationContext)
        val deviceId = DeviceIdentity.get(applicationContext)
        val now = Instant.now()
        val start = now.minus(COLLECTION_LOOKBACK)

        return try {
            collectHealth(queue, deviceId, start, now)
            collectDeviceContext(queue, deviceId)
            Result.success()
        } catch (error: kotlinx.coroutines.CancellationException) {
            throw error
        } catch (_: Exception) {
            // Keep the local queue intact and let WorkManager retry provider
            // failures. Permission opt-outs are handled as successful skips.
            Result.retry()
        }
    }

    private suspend fun collectHealth(
        queue: HealthQueue,
        deviceId: String,
        start: Instant,
        end: Instant,
    ) {
        val status = HealthConnectClient.getSdkStatus(applicationContext, HEALTH_CONNECT_PACKAGE)
        if (status != HealthConnectClient.SDK_AVAILABLE) return
        val client = HealthConnectClient.getOrCreate(applicationContext, HEALTH_CONNECT_PACKAGE)
        val granted = client.permissionController.getGrantedPermissions()
        if (granted.intersect(REQUESTED_PERMISSIONS).isEmpty()) return
        HealthConnectCollector(client, queue, deviceId).collectAll(start, end)
    }

    private fun collectDeviceContext(queue: HealthQueue, deviceId: String) {
        try {
            DeviceTelemetryCollector(applicationContext, queue, deviceId).collect(
                windowMillis = DEVICE_LOOKBACK.toMillis(),
            )
        } catch (_: UsageAccessRequiredException) {
            // Usage Access is an explicit opt-in. A missing grant must not
            // prevent health collection or cause an endless retry loop.
        }
    }

    private companion object {
        const val HEALTH_CONNECT_PACKAGE = "com.google.android.apps.healthdata"
        val COLLECTION_LOOKBACK: Duration = Duration.ofDays(30)
        val DEVICE_LOOKBACK: Duration = Duration.ofHours(24)
        val REQUESTED_PERMISSIONS = setOf(
            androidx.health.connect.client.permission.HealthPermission.getReadPermission(StepsRecord::class),
            androidx.health.connect.client.permission.HealthPermission.getReadPermission(HeartRateRecord::class),
            androidx.health.connect.client.permission.HealthPermission.getReadPermission(SleepSessionRecord::class),
            androidx.health.connect.client.permission.HealthPermission.getReadPermission(ExerciseSessionRecord::class),
        )
    }
}
