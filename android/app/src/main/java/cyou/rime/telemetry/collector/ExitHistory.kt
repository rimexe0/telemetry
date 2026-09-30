package cyou.rime.telemetry.collector

import android.app.ActivityManager
import android.app.ApplicationExitInfo
import android.content.Context
import android.os.Build
import androidx.annotation.RequiresApi
import org.json.JSONObject
import java.time.Instant

/**
 * A force stop kills collection and blocking without any chance to report it.
 * When the app runs again, Android's exit history still has it; this records
 * each user stop once, dated when it happened, so the gap has an explanation.
 */
object ExitHistory {
    private const val PREFS = "telemetry-exit-history"
    private const val KEY_LAST = "last_recorded_ms"

    fun recordNew(context: Context) {
        if (Build.VERSION.SDK_INT >= 30) recordSince(context)
    }

    @RequiresApi(30)
    private fun recordSince(context: Context) {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val last = preferences.getLong(KEY_LAST, 0)
        val exits = context.getSystemService(ActivityManager::class.java)
            .getHistoricalProcessExitReasons(context.packageName, 0, 16)
            .filter { it.timestamp > last && it.processName == context.packageName && userStop(it.reason) }
        if (exits.isEmpty()) return
        val deviceId = DeviceIdentity.get(context)
        val collected = Instant.now().toString()
        HealthQueue(context).enqueue(exits.map { exit ->
            val at = Instant.ofEpochMilli(exit.timestamp).toString()
            LocalObservation(
                sourceInstanceKey = "android:$deviceId:collector",
                sourceKind = "telemetry_collector",
                deviceId = deviceId,
                sourceRecordKey = "stopped:${exit.timestamp}",
                kind = "android.collector_stopped",
                recordType = "collector_stopped",
                occurredAt = at,
                endedAt = null,
                collectedAt = collected,
                sourceSchemaVersion = "android-exit-v1",
                payloadJson = JSONObject()
                    .put("stopped_at", at)
                    .put("reason", reasonName(exit.reason))
                    .put("description", exit.description ?: JSONObject.NULL)
                    .put("blocking_enabled_now", PolicyEvents.blockingEnabled(context))
                    .toString(),
            )
        })
        preferences.edit().putLong(KEY_LAST, exits.maxOf { it.timestamp }).apply()
    }

    @RequiresApi(30)
    private fun userStop(reason: Int): Boolean =
        reason == ApplicationExitInfo.REASON_USER_REQUESTED ||
            (Build.VERSION.SDK_INT >= 34 && reason == ApplicationExitInfo.REASON_USER_STOPPED)

    @RequiresApi(30)
    private fun reasonName(reason: Int): String = when {
        reason == ApplicationExitInfo.REASON_USER_REQUESTED -> "user_requested"
        Build.VERSION.SDK_INT >= 34 && reason == ApplicationExitInfo.REASON_USER_STOPPED -> "user_stopped"
        else -> "reason_$reason"
    }
}
