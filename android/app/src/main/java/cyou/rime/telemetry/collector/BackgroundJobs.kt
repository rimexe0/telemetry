package cyou.rime.telemetry.collector

import android.content.Context
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import java.util.concurrent.TimeUnit

/** Android-scheduled fallback collection and delivery, shared by setup and settings. */
object BackgroundJobs {
    private const val COLLECTION_WORK_NAME = "telemetry-collection"
    private const val DELIVERY_WORK_NAME = "telemetry-delivery"

    fun scheduleCollection(context: Context) {
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(
            COLLECTION_WORK_NAME,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<TelemetryCollectionWorker>(15, TimeUnit.MINUTES).build(),
        )
    }

    fun scheduleDelivery(context: Context) {
        scheduleCollection(context)
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(
            DELIVERY_WORK_NAME,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<TelemetryUploadWorker>(15, TimeUnit.MINUTES)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build(),
        )
    }
}
