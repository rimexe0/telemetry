package cyou.rime.telemetry.collector

import android.content.Context
import android.content.ComponentName
import android.app.NotificationManager
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset
import org.json.JSONObject

data class CollectionCycle(val nextDelayMillis: Long, val summary: String)

/** Shared by the visible service, setup action and OS-scheduled fallback. */
object CollectionRuntime {
    private val lock = Mutex()
    private var resources: CollectorResources? = null
    @Volatile var foregroundActive: Boolean = false
        private set

    fun setRunning(running: Boolean) {
        foregroundActive = running
    }

    private fun preferences(context: Context) =
        context.getSharedPreferences("telemetry-collection-runtime", Context.MODE_PRIVATE)

    fun latest(context: Context): String =
        preferences(context).getString("latest_summary", null) ?: "No collection cycle has run yet."

    suspend fun runCycle(context: Context, backgroundHealth: Boolean, forceHealth: Boolean = false): CollectionCycle =
        withContext(Dispatchers.IO) {
            lock.withLock {
                try {
                    cycle(context.applicationContext, backgroundHealth, forceHealth)
                } catch (error: CancellationException) {
                    throw error
                } catch (error: Exception) {
                    val message = "Collection error: ${error.javaClass.simpleName}. Queue retained."
                    preferences(context).edit().putString("latest_summary", message).apply()
                    CollectionCycle(300_000L, message)
                }
            }
        }

    private suspend fun cycle(context: Context, backgroundHealth: Boolean, forceHealth: Boolean): CollectionCycle {
        val prefs = preferences(context)
        val settings = CollectionSettingsStore(context).current
        val sampler = resources ?: CollectorResources(context).also { resources = it }
        val snapshot = sampler.sample()
        val budget = snapshot.budget(settings)
        val now = System.currentTimeMillis()
        val at = Instant.ofEpochMilli(now).toString()
        val queue = HealthQueue(context)
        val device = DeviceIdentity.get(context)
        val states = JSONObject(prefs.getString("source_states", "{}") ?: "{}")

        if (due(now, prefs.getLong("last_device_at", 0L), budget.intervalMillis)) {
            try {
                val result = DeviceTelemetryCollector(context, queue, device).collect()
                states.put("usage", result.usageState).put("device", "ok")
                prefs.edit().putLong("last_device_at", now).apply()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                states.put("device", "error:${error.javaClass.simpleName}")
            }
        }
        // SMS collection is an independent owner opt-in. A missing READ_SMS
        // grant or telephony provider must not stop the other sources.
        if (SmsCollectionSettings(context).enabled) {
            val result = SmsCollector(context, queue, device).collect(now)
            states.put("sms", result.state)
        } else {
            states.put("sms", SmsCollectionState.DISABLED)
        }
        // Calendars: read-only, every 15 minutes, only what changed. Without
        // the permission it tries again next cycle, so granting it takes effect at once.
        if (due(now, prefs.getLong("last_calendar_at", 0L), CalendarCollector.EVERY_MILLIS)) {
            val state = runCatching { CalendarCollector(context, queue, device).collect() }.getOrElse { "error:${it.javaClass.simpleName}" }
            states.put("calendar", state)
            if (state != "permission_required") prefs.edit().putLong("last_calendar_at", now).apply()
        }
        // Samsung Health read directly: on this phone it does not write to
        // Health Connect, so the watch's data only arrives this way.
        if (SamsungHealthSource.SUPPORTED && due(now, prefs.getLong("last_samsung_health_at", 0L), SamsungHealthRecords.EVERY_MILLIS)) {
            val state = runCatching { SamsungHealthSource.collect(context, queue, device) }.getOrElse { "error:${it.javaClass.simpleName}" }
            states.put("samsung_health", state)
            if (state != "permission_required") prefs.edit().putLong("last_samsung_health_at", now).apply()
        }
        // Each source fails independently. A phone without Health Connect still
        // collects device observations and delivers its existing queue.
        if (forceHealth || due(now, prefs.getLong("last_health_at", 0L), settings.healthMinutes * 60_000L)) {
            try {
                val result = HealthConnectSync(context, queue, device).collect(backgroundHealth)
                result.states.forEach { (type, state) -> states.put(type, state) }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                states.put("health", "error:${error.javaClass.simpleName}")
            }
            prefs.edit().putLong("last_health_at", now).apply()
        }
        val location = context.getSharedPreferences("telemetry-location-status", Context.MODE_PRIVATE)
        val locationState = location.getString("state", "not_started")
        val lastLocationCheck = location.getLong("last_service_check_at", 0L)
        states.put("location", if (locationState !in listOf("not_started", "stopped", "permission_required") &&
            now - lastLocationCheck > 120_000L) "stale_service_check" else locationState)
        states.put("notifications", if (!PolicyStore(context).captureNotifications) "disabled" else
            if (context.getSystemService(NotificationManager::class.java).isNotificationListenerAccessGranted(
                ComponentName(context, TelemetryNotificationListenerService::class.java),
            )) "listening" else "permission_required")
        prefs.edit().putString("source_states", states.toString()).apply()

        val deliveryState = deliver(context, queue, snapshot, budget, settings, now)
        val pending = queue.pendingStats()
        val delivered = queue.deliveryStats()
        val payload = snapshot.toJson()
            .put("source_states", states)
            .put("last_location_fix_at", location.getLong("last_fix_at", 0L).takeIf { it > 0L }?.let { Instant.ofEpochMilli(it).toString() } ?: JSONObject.NULL)
            .put("foreground_active", foregroundActive)
            .put("cycle_interval_seconds", budget.intervalMillis / 1000L)
            .put("health_interval_minutes", settings.healthMinutes)
            .put("constrained_reason", budget.constrainedReason ?: JSONObject.NULL)
            .put("delivery_state", deliveryState)
            .put("pending_revisions", pending.pendingRevisions)
            .put("oldest_pending_collected_at", pending.oldestPendingCollectedAt ?: JSONObject.NULL)
            .put("last_successful_delivery_at", delivered.lastSuccessfulAt ?: JSONObject.NULL)
            .put("attempted_upload_body_bytes", delivered.attemptedBodyBytes)
            .put("failed_upload_batches", delivered.failedBatches)
            .put("metered_body_bytes_today", prefs.getLong("metered_used", 0L))
        // Resource history is small: at most one sample per five minutes.
        if (due(now, prefs.getLong("last_metrics_at", 0L), 300_000L)) {
            queue.enqueue(listOf(LocalObservation(
                sourceInstanceKey = "android:$device:collector",
                sourceKind = "telemetry_collector", deviceId = device,
                sourceRecordKey = "collector:$now", kind = "telemetry.collector_status",
                recordType = "collector_status", occurredAt = at, endedAt = null,
                collectedAt = at, sourceSchemaVersion = "telemetry-collector-status-v1",
                payloadJson = payload.toString(),
            )))
            prefs.edit().putLong("last_metrics_at", now).apply()
        }
        val summary = buildString {
            append("Checked $at\n")
            append("${if (foregroundActive) "Foreground collection" else "Scheduled fallback"}: ${budget.intervalMillis / 1000}s target")
            budget.constrainedReason?.let { append("; slowed: $it") }
            append("\n$deliveryState; ${pending.pendingRevisions} pending")
            append("\nLast upload: ${delivered.lastSuccessfulAt ?: "none"}")
            append("\nBattery: ${snapshot.batteryLevelPercent?.toInt() ?: "unknown"}%, ${snapshot.batteryTemperatureC ?: "unknown"} C")
            append("\nCPU: ${snapshot.processCpuPercent?.let { "%.2f".format(it) } ?: "unknown"}% of all cores")
            append("\nApp traffic since boot: TX ${snapshot.uidTxBytes ?: "unknown"}, RX ${snapshot.uidRxBytes ?: "unknown"} bytes")
            append("\nUpload payload today on metered: ${prefs.getLong("metered_used", 0L) / 1024} KiB / ${settings.meteredDailyMiB} MiB")
            states.keys().forEach { key -> append("\n$key: ${states.optString(key)}") }
        }
        prefs.edit().putString("latest_summary", summary).putString("latest_json", payload.toString()).apply()
        return CollectionCycle(budget.intervalMillis, "${pending.pendingRevisions} queued; $deliveryState")
    }

    private fun deliver(context: Context, queue: HealthQueue, snapshot: ResourceSnapshot,
        budget: CycleBudget, settings: CollectionSettings, now: Long): String {
        val prefs = preferences(context)
        val today = LocalDate.now(ZoneOffset.UTC).toString()
        if (prefs.getString("metered_day", "") != today) {
            prefs.edit().putString("metered_day", today).putLong("metered_used", 0L).commit()
        }
        if (queue.pendingStats().pendingRevisions == 0L) return "queue_empty"
        if (!snapshot.networkAvailable) return "offline"
        val config = DeliveryConfig(context)
        val token = runCatching { config.token() }.getOrNull()
        if (config.serverUrl.isBlank() || token.isNullOrBlank()) return "pairing_required"
        val retryAt = prefs.getLong("retry_at", 0L)
        if (retryAt > now && retryAt - now <= 900_000L) return "retry_backoff"
        val used = prefs.getLong("metered_used", 0L)
        val remaining = settings.meteredDailyMiB * 1024L * 1024L - used
        val limit = if (snapshot.networkMetered) minOf(budget.maxUploadBytes, remaining) else budget.maxUploadBytes
        if (limit <= 0L) return "metered_daily_limit"
        var attemptedBytes = limit
        // Reserve the bounded attempt first. A crash may conservatively consume
        // the allowance; it cannot reset it and trigger unlimited retries.
        if (snapshot.networkMetered) check(prefs.edit().putLong("metered_used", used + limit).commit())
        return try {
            val result = TelemetryDeliveryClient(queue, DeviceIdentity.get(context), config.serverUrl, token)
                .drainBounded(maxBatches = if (budget.constrainedReason == null) 10 else 1,
                    maxBytes = limit, maxMillis = 20_000L)
            attemptedBytes = result.bodyBytes
            prefs.edit().putInt("delivery_failures", 0).putLong("retry_at", 0L).apply()
            if (result.hasMore) "catching_up" else "delivered"
        } catch (error: Exception) {
            // The drain reports attempts even when recording metrics failed.
            // Unknown failures retain the full reservation conservatively.
            if (error is DeliveryException) {
                attemptedBytes = error.partialResult?.bodyBytes
                    ?: if (error.kind == DeliveryFailureKind.OVERSIZED_ROW || error.kind == DeliveryFailureKind.INVALID_PAYLOAD) 0L
                    else error.bodyBytes.takeIf { it > 0L } ?: limit
            }
            val failures = (prefs.getInt("delivery_failures", 0) + 1).coerceAtMost(5)
            prefs.edit().putInt("delivery_failures", failures)
                .putLong("retry_at", now + minOf(900_000L, 60_000L shl (failures - 1))).apply()
            if (error is DeliveryException) "delivery_${error.kind ?: error.status}"
            else "delivery_error:${error.javaClass.simpleName}"
        } finally {
            if (snapshot.networkMetered) {
                val attempted = attemptedBytes.coerceIn(0L, limit)
                prefs.edit().putLong("metered_used", used + attempted).commit()
            }
        }
    }

    private fun due(now: Long, previous: Long, interval: Long): Boolean =
        previous <= 0L || now < previous || now - previous >= interval
}
