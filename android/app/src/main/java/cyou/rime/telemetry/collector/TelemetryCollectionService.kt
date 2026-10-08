package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.IBinder
import androidx.core.content.ContextCompat
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
    private var watchJob: Job? = null
    private var screenOn: BroadcastReceiver? = null

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
            // Explain any gap left by a force stop before collecting again.
            scope.launch { runCatching { ExitHistory.recordNew(this@TelemetryCollectionService) } }
            startWatching()
            registerScreenOn()
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

    /**
     * Keeps one request waiting for the server's answer to change, so a plan,
     * opening or block set by the assistant applies at once rather than at
     * the next collection cycle.
     */
    private fun startWatching() {
        watchJob?.cancel()
        watchJob = scope.launch(Dispatchers.IO) {
            var pause = 5_000L
            while (isActive) {
                val known = NowCache.fingerprint(this@TelemetryCollectionService)
                if (known == null) {
                    runCatching { PolicyWatch.check(this@TelemetryCollectionService) }
                    delay(pause)
                    continue
                }
                when (val body = DeviceApi.waitNow(this@TelemetryCollectionService, known)) {
                    null -> {
                        delay(pause)
                        pause = minOf(pause * 2, 60_000L)
                    }
                    "" -> pause = 5_000L
                    else -> {
                        pause = 5_000L
                        NowCache.store(this@TelemetryCollectionService, body)
                        runCatching { PolicyWatch.check(this@TelemetryCollectionService, fetch = false) }
                    }
                }
            }
        }
    }

    /**
     * Android may hold everything while the screen is off; when it comes on,
     * fetch the answer at once and start a fresh waiting request.
     */
    private fun registerScreenOn() {
        if (screenOn != null) return
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                scope.launch(Dispatchers.IO) {
                    runCatching { PolicyWatch.check(this@TelemetryCollectionService) }
                    startWatching()
                }
            }
        }
        val filter = IntentFilter().apply {
            addAction(Intent.ACTION_SCREEN_ON)
            addAction(Intent.ACTION_USER_PRESENT)
        }
        ContextCompat.registerReceiver(this, receiver, filter, ContextCompat.RECEIVER_NOT_EXPORTED)
        screenOn = receiver
    }

    override fun onDestroy() {
        screenOn?.let { runCatching { unregisterReceiver(it) } }
        screenOn = null
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
