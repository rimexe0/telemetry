package cyou.rime.telemetry.collector

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import java.time.Instant
import org.json.JSONObject

class TelemetryNotificationListenerService : NotificationListenerService() {
    override fun onNotificationPosted(statusBarNotification: StatusBarNotification) {
        if (!PolicyStore(this).captureNotifications) return
        enqueue(statusBarNotification, "posted")
    }

    override fun onNotificationRemoved(statusBarNotification: StatusBarNotification) {
        if (!PolicyStore(this).captureNotifications) return
        enqueue(statusBarNotification, "removed")
    }

    private fun enqueue(statusBarNotification: StatusBarNotification, state: String) {
        val deviceId = DeviceIdentity.get(this)
        val notification = statusBarNotification.notification
        val payload = JSONObject()
            .put("state", state)
            .put("package_name", statusBarNotification.packageName)
            .put("notification_key", statusBarNotification.key)
            .put("notification_id", statusBarNotification.id)
            .put("post_time", Instant.ofEpochMilli(statusBarNotification.postTime).toString())
            .putNullable("title", notification.extras?.getCharSequence(Notification.EXTRA_TITLE)?.toString())
            .putNullable("text", notification.extras?.getCharSequence(Notification.EXTRA_TEXT)?.toString())
        val sourceKey = "notification:${statusBarNotification.key}:$state"
        HealthQueue(this).enqueue(
            listOf(
                LocalObservation(
                    sourceInstanceKey = "android:$deviceId:notifications",
                    sourceKind = "android_notifications",
                    deviceId = deviceId,
                    sourceRecordKey = sourceKey,
                    kind = "android.notification.$state",
                    recordType = "notification",
                    occurredAt = Instant.ofEpochMilli(statusBarNotification.postTime).toString(),
                    endedAt = null,
                    collectedAt = Instant.now().toString(),
                    sourceSchemaVersion = "android-notification-listener-api-35",
                    payloadJson = payload.toString(),
                ),
            ),
        )
    }

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )
}
