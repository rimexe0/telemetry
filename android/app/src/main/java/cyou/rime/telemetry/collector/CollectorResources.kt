package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.TrafficStats
import android.os.BatteryManager
import android.os.Build
import android.os.PowerManager
import android.os.Process
import android.os.SystemClock
import java.io.File
import java.time.Instant
import kotlin.math.max
import kotlin.math.min
import org.json.JSONObject

/** One observed resource sample. Unsupported platform counters remain null. (R-009 and R-050.) */
data class ResourceSnapshot(
    val capturedAtEpochMillis: Long,
    val batteryLevelPercent: Double?,
    val batteryTemperatureC: Double?,
    val charging: Boolean?,
    val powerSaveMode: Boolean,
    val thermalStatus: Int?,
    val uidTxBytes: Long?,
    val uidRxBytes: Long?,
    val uidTxDeltaBytes: Long?,
    val uidRxDeltaBytes: Long?,
    val processCpuPercent: Double?,
    val processRssBytes: Long?,
    val networkAvailable: Boolean,
    val networkValidated: Boolean,
    val networkMetered: Boolean,
) {
    /** Short alias used by collection policy callers. */
    val metered: Boolean
        get() = networkMetered

    /** Active network has the INTERNET capability; validation is separate. */
    val networkConnected: Boolean
        get() = networkAvailable

    fun toJson(): JSONObject = JSONObject()
        .put("captured_at", Instant.ofEpochMilli(capturedAtEpochMillis).toString())
        .putNullable("battery_level_percent", batteryLevelPercent)
        .putNullable("battery_temperature_c", batteryTemperatureC)
        .putNullable("charging", charging)
        .put("power_save_mode", powerSaveMode)
        .putNullable("thermal_status", thermalStatus)
        .putNullable("uid_tx_bytes", uidTxBytes)
        .putNullable("uid_rx_bytes", uidRxBytes)
        .putNullable("uid_tx_delta_bytes", uidTxDeltaBytes)
        .putNullable("uid_rx_delta_bytes", uidRxDeltaBytes)
        .putNullable("process_cpu_percent", processCpuPercent)
        .putNullable("process_rss_bytes", processRssBytes)
        .put("network_available", networkAvailable)
        .put("network_validated", networkValidated)
        .put("network_metered", networkMetered)

    fun budget(settings: CollectionSettings): CycleBudget = CollectionBudget.decide(settings, this)

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )
}

/**
 * Samples only local platform counters. The instance should be retained by a
 * collector service so CPU and traffic deltas remain meaningful across ticks.
 */
class CollectorResources(context: Context) {
    private val context = context.applicationContext
    private var previousCpuMillis: Long? = null
    private var previousWallMillis: Long? = null
    private var previousTxBytes: Long? = null
    private var previousRxBytes: Long? = null

    fun sample(): ResourceSnapshot {
        val wallMillis = SystemClock.elapsedRealtime()
        val capturedAt = System.currentTimeMillis()
        val battery = context.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val power = context.getSystemService(PowerManager::class.java)
        val connectivity = context.getSystemService(ConnectivityManager::class.java)
        val capabilities = connectivity.activeNetwork?.let(connectivity::getNetworkCapabilities)

        val txBytes = TrafficStats.getUidTxBytes(Process.myUid()).takeIf { it >= 0L }
        val rxBytes = TrafficStats.getUidRxBytes(Process.myUid()).takeIf { it >= 0L }
        val cpuMillis = Process.getElapsedCpuTime()
        val cpuPercent = previousCpuMillis?.let { oldCpu ->
            previousWallMillis?.let { oldWall ->
                val cpuDelta = cpuMillis - oldCpu
                val wallDelta = wallMillis - oldWall
                if (cpuDelta >= 0L && wallDelta > 0L) {
                    val cores = Runtime.getRuntime().availableProcessors().coerceAtLeast(1)
                    (cpuDelta.toDouble() * 100.0 / (wallDelta * cores)).coerceIn(0.0, 100.0)
                } else {
                    null
                }
            }
        }
        val snapshot = ResourceSnapshot(
            capturedAtEpochMillis = capturedAt,
            batteryLevelPercent = battery?.batteryLevelPercent(),
            batteryTemperatureC = battery?.batteryTemperatureC(),
            charging = battery?.isCharging(),
            powerSaveMode = power?.isPowerSaveMode == true,
            thermalStatus = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                power?.currentThermalStatus
            } else {
                null
            },
            uidTxBytes = txBytes,
            uidRxBytes = rxBytes,
            uidTxDeltaBytes = positiveDelta(previousTxBytes, txBytes),
            uidRxDeltaBytes = positiveDelta(previousRxBytes, rxBytes),
            processCpuPercent = cpuPercent,
            processRssBytes = readProcessRssBytes(),
            networkAvailable = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true,
            networkValidated = capabilities?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true,
            networkMetered = connectivity.isActiveNetworkMetered,
        )
        previousCpuMillis = cpuMillis
        previousWallMillis = wallMillis
        previousTxBytes = txBytes
        previousRxBytes = rxBytes
        return snapshot
    }

    private fun readProcessRssBytes(): Long? = try {
        File("/proc/${Process.myPid()}/status").useLines { lines ->
            lines.firstNotNullOfOrNull { line ->
                if (!line.startsWith("VmRSS:")) return@firstNotNullOfOrNull null
                line.substringAfter(':').trim().split(Regex("\\s+"), limit = 2)
                    .firstOrNull()?.toLongOrNull()?.times(1024L)
            }
        }
    } catch (_: Exception) {
        null
    }

    private companion object {
        fun positiveDelta(previous: Long?, current: Long?): Long? =
            if (previous != null && current != null && current >= previous) current - previous else null

        fun Intent.batteryLevelPercent(): Double? {
            val level = getIntExtra(BatteryManager.EXTRA_LEVEL, -1)
            val scale = getIntExtra(BatteryManager.EXTRA_SCALE, -1)
            return if (level >= 0 && scale > 0) (level.toDouble() * 100.0 / scale).coerceIn(0.0, 100.0) else null
        }

        fun Intent.batteryTemperatureC(): Double? = getIntExtra(
            BatteryManager.EXTRA_TEMPERATURE,
            Int.MIN_VALUE,
        ).takeIf { it != Int.MIN_VALUE }?.toDouble()?.div(10.0)

        fun Intent.isCharging(): Boolean {
            val status = getIntExtra(BatteryManager.EXTRA_STATUS, -1)
            val plugged = getIntExtra(BatteryManager.EXTRA_PLUGGED, 0)
            return status == BatteryManager.BATTERY_STATUS_CHARGING ||
                status == BatteryManager.BATTERY_STATUS_FULL ||
                plugged != 0
        }
    }
}
