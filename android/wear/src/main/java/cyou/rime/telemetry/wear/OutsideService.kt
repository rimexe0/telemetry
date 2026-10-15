package cyou.rime.telemetry.wear

import android.Manifest
import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import com.google.android.gms.wearable.CapabilityClient
import com.google.android.gms.wearable.CapabilityInfo
import com.google.android.gms.wearable.PutDataMapRequest
import com.google.android.gms.wearable.Wearable
import java.io.File
import java.time.Instant
import java.util.UUID
import org.json.JSONObject

/**
 * Runs all the time on the watch, quietly. When the phone goes out of reach
 * (left at home while the owner went out), it records a walk: GPS while
 * walking, whether the watch is on the wrist, heart rate and steps. When the
 * phone is back in reach the walk goes to it, and from there to the server,
 * which decides what counts as outside. A walk can also be started and
 * stopped by hand.
 */
class OutsideService : Service(), SensorEventListener {
    companion object {
        const val PHONE_CAPABILITY = "telemetry_phone"
        const val ACTION_START = "cyou.rime.telemetry.wear.START_WALK"
        const val ACTION_STOP = "cyou.rime.telemetry.wear.STOP_WALK"
        private const val CHANNEL = "telemetry-outside"
        private const val NOTIFICATION_ID = 5100
        private const val TAG = "TelemetryOutside"
        /** The link drops for a moment at times; a walk starts only if it stays gone. */
        private const val OUT_OF_REACH_MILLIS = 60_000L
        private const val BACK_IN_REACH_MILLIS = 20_000L
        private const val MANUAL_LIMIT_MILLIS = 4 * 60 * 60_000L
        private const val GPS_INTERVAL_MILLIS = 15_000L

        @Volatile var state: String = "starting"
            private set
        @Volatile var current: WalkLog? = null
            private set

        fun start(context: Context, action: String? = null) {
            context.startForegroundService(Intent(context, OutsideService::class.java).setAction(action))
        }
    }

    private val handler = Handler(Looper.getMainLooper())
    private lateinit var sensors: SensorManager
    private lateinit var locations: LocationManager
    private var phoneReachable = true
    private var gpsRunning = false
    private var onBody: Boolean? = null
    private var lastHeartRate: Int? = null
    private var lastHeartRateAt: Instant? = null
    private var stepBase: Float? = null
    private var stepsSeen: Float? = null
    private var lastStepsAt: Instant = Instant.now()
    private val walkFile by lazy { File(filesDir, "current-walk.json") }

    private val capabilityListener = CapabilityClient.OnCapabilityChangedListener { info -> onPhone(info) }
    private val locationListener = LocationListener { location -> onLocation(location) }
    private val confirmAway = Runnable { if (!phoneReachable && current == null) beginWalk("phone_out_of_reach") }
    private val confirmBack = Runnable { if (phoneReachable && current?.trigger == "phone_out_of_reach") endWalk() }
    private val tick = object : Runnable {
        override fun run() {
            pace()
            handler.postDelayed(this, 60_000L)
        }
    }

    override fun onCreate() {
        super.onCreate()
        sensors = getSystemService(SensorManager::class.java)
        locations = getSystemService(LocationManager::class.java)
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Telemetry outside", NotificationManager.IMPORTANCE_LOW))
        if (!granted(Manifest.permission.ACCESS_FINE_LOCATION)) {
            state = "Location not allowed"
            stopSelf()
            return
        }
        goForeground()
        // A walk cut off by a restart is sent as it was.
        if (walkFile.exists()) {
            runCatching { send(JSONObject(walkFile.readText())) }
            walkFile.delete()
        }
        sensors.getDefaultSensor(Sensor.TYPE_LOW_LATENCY_OFFBODY_DETECT, true)?.let { sensors.registerListener(this, it, SensorManager.SENSOR_DELAY_NORMAL) }
        Wearable.getCapabilityClient(this).addListener(capabilityListener, PHONE_CAPABILITY)
        Wearable.getCapabilityClient(this).getCapability(PHONE_CAPABILITY, CapabilityClient.FILTER_REACHABLE).addOnSuccessListener { onPhone(it) }
        handler.post(tick)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> if (current == null) beginWalk("manual")
            ACTION_STOP -> endWalk()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        Wearable.getCapabilityClient(this).removeListener(capabilityListener)
        sensors.unregisterListener(this)
        stopGps()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun onPhone(info: CapabilityInfo) {
        val reachable = info.nodes.isNotEmpty()
        if (reachable == phoneReachable) return
        phoneReachable = reachable
        Log.i(TAG, "phone reachable=$reachable")
        handler.removeCallbacks(confirmAway)
        handler.removeCallbacks(confirmBack)
        if (reachable) handler.postDelayed(confirmBack, BACK_IN_REACH_MILLIS) else handler.postDelayed(confirmAway, OUT_OF_REACH_MILLIS)
        update()
    }

    private fun beginWalk(trigger: String) {
        val now = Instant.now()
        current = WalkLog(UUID.randomUUID().toString(), now, trigger)
        lastStepsAt = now
        stepBase = null
        sensors.getDefaultSensor(Sensor.TYPE_STEP_COUNTER)?.let { sensors.registerListener(this, it, SensorManager.SENSOR_DELAY_NORMAL) }
        if (granted(heartRatePermission())) {
            sensors.getDefaultSensor(Sensor.TYPE_HEART_RATE)?.let { sensors.registerListener(this, it, SensorManager.SENSOR_DELAY_NORMAL) }
        }
        startGps()
        Log.i(TAG, "walk started ($trigger)")
        update()
    }

    private fun endWalk() {
        val walk = current ?: return
        current = null
        walk.end(Instant.now())
        stopGps()
        sensors.getDefaultSensor(Sensor.TYPE_STEP_COUNTER)?.let { sensors.unregisterListener(this, it) }
        sensors.getDefaultSensor(Sensor.TYPE_HEART_RATE)?.let { sensors.unregisterListener(this, it) }
        if (walk.size >= 2) send(walk.toJson())
        walkFile.delete()
        Log.i(TAG, "walk ended: ${walk.size} points, ${walk.steps} steps")
        update()
    }

    /** Hands the walk to the phone. The watch link keeps it until the phone has it, however long that takes. */
    private fun send(walk: JSONObject) {
        val request = PutDataMapRequest.create("/outside/" + walk.getString("session_id")).apply {
            dataMap.putString("walk", walk.toString())
            dataMap.putLong("sent_at", System.currentTimeMillis())
        }.asPutDataRequest().setUrgent()
        Wearable.getDataClient(this).putDataItem(request)
    }

    private fun pace() {
        val walk = current ?: return
        if (walk.trigger == "manual" && Instant.now().isAfter(walk.startedAt.plusMillis(MANUAL_LIMIT_MILLIS))) {
            endWalk()
            return
        }
        // GPS only while walking: a far room of the house also drops the link.
        if (GpsPacing.shouldRun(lastStepsAt, Instant.now())) startGps() else stopGps()
        update()
    }

    @SuppressLint("MissingPermission")
    private fun startGps() {
        if (gpsRunning || !granted(Manifest.permission.ACCESS_FINE_LOCATION)) return
        runCatching {
            locations.requestLocationUpdates(LocationManager.GPS_PROVIDER, GPS_INTERVAL_MILLIS, 10f, locationListener, Looper.getMainLooper())
            gpsRunning = true
        }.onFailure { Log.w(TAG, "gps: $it") }
    }

    private fun stopGps() {
        if (!gpsRunning) return
        runCatching { locations.removeUpdates(locationListener) }
        gpsRunning = false
    }

    private fun onLocation(location: Location) {
        val walk = current ?: return
        val now = Instant.ofEpochMilli(location.time)
        walk.add(WalkPoint(now, location.latitude, location.longitude, location.accuracy,
            Wearing.worn(onBody, lastHeartRateAt, Instant.now()), lastHeartRate?.takeIf { lastHeartRateAt?.let { Instant.now().epochSecond - it.epochSecond < 90 } == true }))
        // Kept on disk too, so a restart mid-walk loses nothing.
        runCatching { walkFile.writeText(walk.toJson().put("end", now.toString()).toString()) }
        update()
    }

    override fun onSensorChanged(event: SensorEvent) {
        when (event.sensor.type) {
            Sensor.TYPE_LOW_LATENCY_OFFBODY_DETECT -> onBody = event.values[0] > 0.5f
            Sensor.TYPE_HEART_RATE -> if (event.values[0] > 0 && event.accuracy >= SensorManager.SENSOR_STATUS_ACCURACY_LOW) {
                lastHeartRate = event.values[0].toInt()
                lastHeartRateAt = Instant.now()
            }
            Sensor.TYPE_STEP_COUNTER -> {
                val total = event.values[0]
                val base = stepBase ?: total.also { stepBase = it }
                val previous = stepsSeen ?: base
                if (total - previous >= GpsPacing.RESUME_STEPS || (total > previous && gpsRunning)) {
                    lastStepsAt = Instant.now()
                    if (!gpsRunning && current != null) startGps()
                }
                current?.addSteps((total - previous).toLong())
                stepsSeen = total
            }
        }
    }

    override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {}

    private fun heartRatePermission(): String =
        if (Build.VERSION.SDK_INT >= 36) "android.permission.health.READ_HEART_RATE" else Manifest.permission.BODY_SENSORS

    private fun granted(permission: String) = checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED

    private fun update() {
        val walk = current
        state = when {
            walk != null -> "Walk: ${walk.wornMinutes()} min" + if (gpsRunning) "" else " (paused, no steps)"
            !phoneReachable -> "Phone out of reach"
            else -> "Phone nearby"
        }
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, notification())
    }

    private fun goForeground() {
        // Without location there is nothing to record; the screen asks for it first.
        startForeground(NOTIFICATION_ID, notification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION)
    }

    private fun notification(): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, WatchActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_menu_mylocation)
            .setContentTitle("Telemetry")
            .setContentText(state)
            .setOngoing(true)
            .setContentIntent(open)
            .build()
    }
}
