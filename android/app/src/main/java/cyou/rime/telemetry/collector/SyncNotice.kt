package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import java.time.Instant
import java.time.ZoneId

/**
 * Says so when the phone has not reached the server for a while, with a
 * "Sync now" button, instead of piling records up quietly (owner request
 * 2026-11-22, after seven silent hours). It goes away once a sync succeeds.
 */
object SyncNotice {
    private const val CHANNEL = "telemetry-connection"
    private const val ID = 7302
    private const val ACTION_SYNC = "cyou.rime.telemetry.collector.SYNC_NOW"
    private const val WORK = "telemetry-sync-now"

    /** How long records may wait unsent before the owner is told. */
    const val AFTER_MILLIS = 30L * 60L * 1000L

    /**
     * Whether to show the notice: records are waiting and nothing has gone
     * through for [AFTER_MILLIS], counted from the last sync, or from the
     * oldest waiting record when the phone never synced.
     */
    fun due(pending: Long, lastSyncMillis: Long?, oldestPendingMillis: Long?, now: Long): Boolean {
        if (pending <= 0L) return false
        val since = lastSyncMillis ?: oldestPendingMillis ?: return false
        return now - since >= AFTER_MILLIS
    }

    fun update(context: Context, pending: Long, lastSyncAt: String?, oldestPendingAt: String?, now: Long) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        val lastSync = lastSyncAt?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() }
        val oldest = oldestPendingAt?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() }
        if (!due(pending, lastSync, oldest, now)) {
            manager.cancel(ID)
            return
        }
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Connection to your server", NotificationManager.IMPORTANCE_DEFAULT))
        val since = lastSync?.let { Instant.ofEpochMilli(it).atZone(ZoneId.systemDefault()) }
        val title = if (since != null) String.format("Not synced since %02d:%02d", since.hour, since.minute) else "Not synced yet"
        val way = if (SealedChannel.configured(context) == null) {
            " This phone only reaches the server over Tailscale; turn it on, or it learns the relay at its next sync."
        } else ""
        val text = "$pending records are waiting on the phone; nothing is lost.$way"
        val sync = PendingIntent.getBroadcast(context, 5, Intent(context, SyncNowReceiver::class.java).setAction(ACTION_SYNC), PendingIntent.FLAG_IMMUTABLE)
        runCatching {
            manager.notify(ID, Notification.Builder(context, CHANNEL)
                .setSmallIcon(android.R.drawable.stat_notify_sync_noanim)
                .setContentTitle(title)
                .setContentText(text)
                .setStyle(Notification.BigTextStyle().bigText(text))
                .setOnlyAlertOnce(true)
                .addAction(Notification.Action.Builder(null, "Sync now", sync).build())
                .setContentIntent(sync)
                .build())
        }
    }

    /** Clears the wait after failures and syncs as soon as there is a network. */
    fun syncNow(context: Context) {
        CollectionRuntime.clearRetryWait(context)
        WorkManager.getInstance(context).enqueueUniqueWork(
            WORK,
            ExistingWorkPolicy.REPLACE,
            OneTimeWorkRequestBuilder<TelemetryUploadWorker>()
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build(),
        )
    }
}

class SyncNowReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        SyncNotice.syncNow(context.applicationContext)
    }
}
