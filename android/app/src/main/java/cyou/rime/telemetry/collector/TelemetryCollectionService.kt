package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.IBinder
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** Owner-started device activity logging. The OS may still suspend execution. */
class TelemetryCollectionService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var collectionJob: Job? = null

    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Telemetry collection", NotificationManager.IMPORTANCE_LOW),
        )
        startForeground(ID, notification("Collecting approved device sources"))
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return START_NOT_STICKY
        }
        if (collectionJob?.isActive != true) {
            CollectionRuntime.setRunning(true)
            collectionJob = scope.launch {
                while (isActive) {
                    val cycle = CollectionRuntime.runCycle(this@TelemetryCollectionService, backgroundHealth = true)
                    runCatching { withContext(Dispatchers.IO) { PolicyWatch.check(this@TelemetryCollectionService) } }
                    getSystemService(NotificationManager::class.java).notify(ID, notification(cycle.summary))
                    delay(cycle.nextDelayMillis)
                }
            }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        scope.cancel()
        CollectionRuntime.setRunning(false)
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun notification(message: String): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, 1,
            Intent(this, TelemetryCollectionService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE)
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_menu_recent_history)
            .setContentTitle("Telemetry collection active")
            .setContentText(message)
            .setContentIntent(open)
            .addAction(Notification.Action.Builder(android.R.drawable.ic_media_pause, "Stop", stop).build())
            .setOngoing(true)
            .build()
    }

    companion object {
        const val ACTION_STOP = "cyou.rime.telemetry.collector.STOP_COLLECTION"
        private const val CHANNEL = "telemetry-collection"
        private const val ID = 4102
    }
}
