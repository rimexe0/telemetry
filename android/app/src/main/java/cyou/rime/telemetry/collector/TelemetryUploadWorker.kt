package cyou.rime.telemetry.collector

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

/** Uses the same bounded delivery, metered allowance and retry backoff as the service. */
class TelemetryUploadWorker(context: Context, workerParameters: WorkerParameters) :
    CoroutineWorker(context, workerParameters) {
    override suspend fun doWork(): Result {
        CollectionRuntime.runCycle(applicationContext, backgroundHealth = true)
        return Result.success()
    }
}
