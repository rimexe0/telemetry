package cyou.rime.telemetry.collector

import android.Manifest
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat

/**
 * Keeps collection running once setup has started it. App updates and reboots
 * stop the foreground services and nothing brings them back by itself, so this
 * is called after an update, after boot, when the app opens and when Android
 * reconnects the blocking service.
 */
object CollectionStarter {
    fun ensureRunning(context: Context) {
        val app = context.applicationContext
        if (DeliveryConfig(app).token() == null) return
        if (!app.getSharedPreferences("telemetry-setup", Context.MODE_PRIVATE).getBoolean("collection_started", false)) return
        BackgroundJobs.scheduleDelivery(app)
        if (!CollectionRuntime.foregroundActive) {
            runCatching { ContextCompat.startForegroundService(app, Intent(app, TelemetryCollectionService::class.java)) }
        }
        val location = listOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION)
            .any { ContextCompat.checkSelfPermission(app, it) == PackageManager.PERMISSION_GRANTED }
        if (location) {
            runCatching { ContextCompat.startForegroundService(app, Intent(app, TelemetryLocationService::class.java)) }
        }
    }
}

/** Restarts collection after the app is updated or the phone restarts. */
class RestartReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> CollectionStarter.ensureRunning(context)
        }
    }
}
