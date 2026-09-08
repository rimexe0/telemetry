package cyou.rime.telemetry.collector

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

class TelemetryUploadWorker(
    context: Context,
    workerParameters: WorkerParameters,
) : CoroutineWorker(context, workerParameters) {
    override suspend fun doWork(): Result {
        val config = DeliveryConfig(applicationContext)
        val url = config.serverUrl
        val token = runCatching { config.token() }.getOrNull()
        if (url.isBlank() || token.isNullOrBlank()) return Result.success()
        return try {
            TelemetryDeliveryClient(
                HealthQueue(applicationContext),
                DeviceIdentity.get(applicationContext),
                url,
                token,
            ).drainOnce()
            Result.success()
        } catch (error: DeliveryException) {
            if (error.status in 400..499) Result.failure() else Result.retry()
        } catch (_: Exception) {
            Result.retry()
        }
    }
}
