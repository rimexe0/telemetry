package cyou.rime.telemetry.collector

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import java.time.Instant
import org.json.JSONObject

/**
 * Removes notifications from apps that are blocked right now: Android hides a
 * suspended app's notifications, but on 2026-10-06 an X notification still
 * showed on waking while X was suspended. Only explicitly blocked apps are
 * touched (the same set as root suspension), never calls, messages or system
 * apps. Separately, notifications are recorded only when the owner turned
 * that on.
 */
class TelemetryNotificationListenerService : NotificationListenerService() {
    override fun onListenerConnected() {
        connected = this
        quietBlocked()
    }

    override fun onListenerDisconnected() {
        if (connected === this) connected = null
    }

    override fun onNotificationPosted(statusBarNotification: StatusBarNotification) {
        quiet(statusBarNotification, blocked())
        if (!PolicyStore(this).captureNotifications) return
        enqueue(statusBarNotification, "posted")
    }

    /** The blocked apps now, from the cached decision; empty when nothing is blocked. */
    private fun blocked(): Set<String> {
        val entry = NowCache.read(this) ?: return emptySet()
        val decision = BlockPolicy.current(this, entry.json)
        return AppSuspender.wanted(decision, { true }, PolicyEvents.systemSafe(this), packageName)
    }

    private fun quiet(notification: StatusBarNotification, blocked: Set<String>) {
        if (notification.packageName !in blocked) return
        runCatching { cancelNotification(notification.key) }
        // Which app tried, never what it said.
        PolicyEvents.record(this, "android.policy_notification_removed", "notification-removed:${notification.key}:${notification.postTime}", null,
            mapOf("package" to notification.packageName))
    }

    /** Removes what blocked apps are already showing, as a block starts. */
    fun quietBlocked() {
        val blocked = blocked()
        if (blocked.isEmpty()) return
        runCatching { activeNotifications }.getOrNull()?.forEach { quiet(it, blocked) }
    }

    companion object {
        @Volatile var connected: TelemetryNotificationListenerService? = null
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
