package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent

/** Tells the person once that the panel removed this phone, instead of failing quietly. */
object RemovedNotice {
    private const val CHANNEL = "telemetry-connection"
    private const val ID = 7301

    fun show(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Connection to your server", NotificationManager.IMPORTANCE_DEFAULT))
        val open = PendingIntent.getActivity(context, 4, SetupActivity.intent(context, SetupStep.PAIR), PendingIntent.FLAG_IMMUTABLE)
        val text = "It was removed in the panel, so it stopped sending. Tap to connect it again with a new setup code. What it records meanwhile stays on the phone."
        runCatching {
            manager.notify(ID, Notification.Builder(context, CHANNEL)
                .setSmallIcon(android.R.drawable.stat_notify_error)
                .setContentTitle("This phone was removed from Telemetry")
                .setContentText(text)
                .setStyle(Notification.BigTextStyle().bigText(text))
                .setContentIntent(open)
                .setAutoCancel(true)
                .build())
        }
    }
}
