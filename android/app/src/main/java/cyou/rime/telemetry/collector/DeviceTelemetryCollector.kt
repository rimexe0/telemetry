package cyou.rime.telemetry.collector

import android.app.AppOpsManager
import android.os.Process
import android.app.KeyguardManager
import android.app.usage.UsageEvents
import android.app.usage.UsageStatsManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.PowerManager
import java.time.Instant
import org.json.JSONArray
import org.json.JSONObject

data class DeviceCollectionResult(
    val eventsRead: Int,
    val insertedRevisions: Int,
    val usageState: String = "ok",
)

class UsageAccessRequiredException : IllegalStateException(
    "Usage Access must be enabled before foreground-app telemetry can be read",
)

/** Collects non-content device context into the same append-only local queue. */
class DeviceTelemetryCollector(
    private val context: Context,
    private val queue: HealthQueue,
    private val deviceId: String,
) {
    fun collect(windowMillis: Long? = null): DeviceCollectionResult = synchronized(collectionLock) {
        val now = System.currentTimeMillis()
        val cursors = context.getSharedPreferences("telemetry-usage-cursor", Context.MODE_PRIVATE)
        val begin = windowMillis?.let { now - it }
            ?: UsageCollectionWindow.begin(now, cursors.getLong("last-success", 0L))
        val usage = context.getSystemService(UsageStatsManager::class.java)
        val allowed = context.getSystemService(AppOpsManager::class.java).checkOpNoThrow(
            AppOpsManager.OPSTR_GET_USAGE_STATS, Process.myUid(), context.packageName,
        ) == AppOpsManager.MODE_ALLOWED
        var usageState = if (allowed) "ok" else "permission_required"
        val usageEvents = if (allowed) try {
            usage.queryEvents(begin, now).also { if (it == null) usageState = "unavailable" }
        } catch (_: Exception) {
            usageState = "error"
            null
        } else null
        val observations = mutableListOf<LocalObservation>()
        val event = UsageEvents.Event()
        var eventsRead = 0
        while (usageEvents?.hasNextEvent() == true) {
            usageEvents.getNextEvent(event)
            if (event.packageName.isNullOrBlank()) continue
            if (event.eventType !in INTERESTING_USAGE_EVENTS) continue
            val timestamp = event.timeStamp
            val eventType = usageEventName(event.eventType)
            val packageName = event.packageName
            val className = event.className.orEmpty()
            val payload = JSONObject()
                .put("event_type", eventType)
                .put("package_name", packageName)
                .put("class_name", className)
                .put("event_time", Instant.ofEpochMilli(timestamp).toString())
            observations += LocalObservation(
                sourceInstanceKey = "android:$deviceId:usage",
                sourceKind = "android_usage",
                deviceId = deviceId,
                sourceRecordKey = "usage:$packageName:$timestamp:$eventType:$className",
                kind = "android.usage.$eventType",
                recordType = "usage_event",
                occurredAt = Instant.ofEpochMilli(timestamp).toString(),
                endedAt = null,
                collectedAt = Instant.ofEpochMilli(now).toString(),
                sourceSchemaVersion = "android-usage-events-api-35",
                payloadJson = payload.toString(),
            )
            eventsRead++
        }
        observations += deviceStateObservation(now)
        val inserted = queue.enqueue(observations).insertedRevisions
        // Advance only after all observations are durably queued. A crash replays
        // the overlap, which the queue deduplicates by source identity and bytes.
        if (usageState == "ok" && !cursors.edit().putLong("last-success", now).commit()) {
            usageState = "cursor_write_error"
        }
        DeviceCollectionResult(eventsRead, inserted, usageState)
    }

    @Suppress("DEPRECATION")
    private fun wifiAddress(connectivity: ConnectivityManager): String? = runCatching {
        connectivity.allNetworks.firstNotNullOfOrNull { network ->
            if (connectivity.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) != true) return@firstNotNullOfOrNull null
            connectivity.getLinkProperties(network)?.linkAddresses
                ?.map { it.address }
                ?.firstOrNull { it is java.net.Inet4Address && it.isSiteLocalAddress }
                ?.hostAddress
        }
    }.getOrNull()

    private fun deviceStateObservation(now: Long): LocalObservation {
        val bucket = now / STATE_BUCKET_MILLIS * STATE_BUCKET_MILLIS
        val battery = context.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val power = context.getSystemService(PowerManager::class.java)
        val keyguard = context.getSystemService(KeyguardManager::class.java)
        val connectivity = context.getSystemService(ConnectivityManager::class.java)
        val capabilities = connectivity.activeNetwork?.let(connectivity::getNetworkCapabilities)
        val transports = JSONArray().apply {
            if (capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true) put("wifi")
            if (capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true) put("cellular")
            if (capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) == true) put("ethernet")
            if (capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true) put("vpn")
        }
        val payload = JSONObject()
            .put("captured_at", Instant.ofEpochMilli(now).toString())
            .put("screen_interactive", power.isInteractive)
            .put("keyguard_locked", keyguard.isKeyguardLocked)
            .put("network_available", capabilities != null)
            .put("transports", transports)
            // The phone's address on the Wi-Fi it is on, even while mobile data
            // carries traffic, so the Mac can find it for wireless debugging.
            .putNullable("wifi_ipv4", wifiAddress(connectivity))
            .putNullable("battery_level", battery?.let {
                val scale = it.getIntExtra(BatteryManager.EXTRA_SCALE, 100)
                it.getIntExtra(BatteryManager.EXTRA_LEVEL, -1).takeIf { level -> level >= 0 }
                    ?.let { level -> level.toDouble() / scale }
            })
            .putNullable("battery_status", battery?.getIntExtra(BatteryManager.EXTRA_STATUS, -1))
            .putNullable("battery_plugged", battery?.getIntExtra(BatteryManager.EXTRA_PLUGGED, -1))
            .putNullable("battery_temperature_c", battery?.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, -1)
                ?.takeIf { temperature -> temperature >= 0 }
                ?.let { temperature -> temperature / 10.0 })
        return LocalObservation(
            sourceInstanceKey = "android:$deviceId:state",
            sourceKind = "android_device",
            deviceId = deviceId,
            sourceRecordKey = "state:$bucket",
            kind = "android.device_state",
            recordType = "device_state",
            occurredAt = Instant.ofEpochMilli(bucket).toString(),
            endedAt = null,
            collectedAt = Instant.ofEpochMilli(now).toString(),
            sourceSchemaVersion = "android-device-state-api-35",
            payloadJson = payload.toString(),
        )
    }

    private companion object {
        val collectionLock = Any()
        const val STATE_BUCKET_MILLIS = 60_000L
        val INTERESTING_USAGE_EVENTS = setOf(
            UsageEvents.Event.ACTIVITY_RESUMED,
            UsageEvents.Event.ACTIVITY_PAUSED,
            UsageEvents.Event.MOVE_TO_FOREGROUND,
            UsageEvents.Event.MOVE_TO_BACKGROUND,
        )

        fun usageEventName(eventType: Int): String = when (eventType) {
            UsageEvents.Event.ACTIVITY_RESUMED -> "activity_resumed"
            UsageEvents.Event.ACTIVITY_PAUSED -> "activity_paused"
            UsageEvents.Event.MOVE_TO_FOREGROUND -> "move_to_foreground"
            UsageEvents.Event.MOVE_TO_BACKGROUND -> "move_to_background"
            else -> "unknown"
        }

        fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
            key,
            value ?: JSONObject.NULL,
        )
    }
}
