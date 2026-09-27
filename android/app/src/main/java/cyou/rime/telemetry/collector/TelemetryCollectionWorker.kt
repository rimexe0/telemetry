package cyou.rime.telemetry.collector

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

/** OS-scheduled fallback; foreground collection uses the same serialized cycle. */
class TelemetryCollectionWorker(context: Context, workerParameters: WorkerParameters) :
    CoroutineWorker(context, workerParameters) {
    override suspend fun doWork(): Result {
        CollectionRuntime.runCycle(applicationContext, backgroundHealth = true)
        runCatching { PolicyWatch.check(applicationContext) }
        CollectionStarter.ensureRunning(applicationContext)
        return Result.success()
    }
}
