package cyou.rime.telemetry.collector

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.os.IBinder
import androidx.core.app.ActivityCompat
import java.time.Instant
import org.json.JSONObject

class TelemetryLocationService : Service() {
    private lateinit var locationManager: LocationManager
    private lateinit var listener: LocationListener

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        startForeground(NOTIFICATION_ID, notification())
        locationManager = getSystemService(LocationManager::class.java)
        listener = LocationListener { location -> enqueue(location) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopLocationUpdates()
            stopForeground(true)
            stopSelf()
            return START_NOT_STICKY
        }
        if (!hasLocationPermission()) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (ActivityCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            ActivityCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED
        ) {
            locationManager.requestLocationUpdates(
                LocationManager.GPS_PROVIDER,
                60_000L,
                25f,
                listener,
                mainLooper,
            )
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopLocationUpdates()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun enqueue(location: Location) {
        val deviceId = DeviceIdentity.get(this)
        val payload = JSONObject()
            .put("latitude", location.latitude)
            .put("longitude", location.longitude)
            .put("accuracy_meters", location.accuracy)
            .put("provider", location.provider ?: JSONObject.NULL)
            .put("location_time", Instant.ofEpochMilli(location.time).toString())
            .putNullable("altitude_meters", if (location.hasAltitude()) location.altitude else null)
            .putNullable("speed_meters_per_second", if (location.hasSpeed()) location.speed else null)
            .putNullable("bearing_degrees", if (location.hasBearing()) location.bearing else null)
        HealthQueue(this).enqueue(
            listOf(
                LocalObservation(
                    sourceInstanceKey = "android:$deviceId:location",
                    sourceKind = "android_location",
                    deviceId = deviceId,
                    sourceRecordKey = "location:${location.time}:${location.latitude}:${location.longitude}",
                    kind = "android.location.fix",
                    recordType = "location_fix",
                    occurredAt = Instant.ofEpochMilli(location.time).toString(),
                    endedAt = null,
                    collectedAt = Instant.now().toString(),
                    sourceSchemaVersion = "android-location-manager-api-35",
                    payloadJson = payload.toString(),
                ),
            ),
        )
    }

    private fun stopLocationUpdates() {
        if (::locationManager.isInitialized && ::listener.isInitialized) {
            runCatching { locationManager.removeUpdates(listener) }
        }
    }

    private fun hasLocationPermission(): Boolean =
        ActivityCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            ActivityCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun createNotificationChannel() {
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "Telemetry location", NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun notification(): Notification = Notification.Builder(this, CHANNEL_ID)
        .setContentTitle("Telemetry location collection")
        .setContentText("Location stays in the device queue until delivery is configured.")
        .setSmallIcon(android.R.drawable.ic_menu_mylocation)
        .setOngoing(true)
        .build()

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )

    private companion object {
        const val CHANNEL_ID = "telemetry-location"
        const val NOTIFICATION_ID = 4101
        const val ACTION_STOP = "cyou.rime.telemetry.collector.STOP_LOCATION"
    }
}
