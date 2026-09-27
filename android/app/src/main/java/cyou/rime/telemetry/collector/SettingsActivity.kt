package cyou.rime.telemetry.collector

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.os.Build
import android.provider.Settings
import android.text.InputType
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.health.connect.client.HealthConnectFeatures
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.PermissionController
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.lifecycle.lifecycleScope
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

/**
 * Owner-operated setup for the Android health collector. It requests only the
 * records that the first collector can preserve and never reads or uploads
 * health data until the owner has granted consent and provisioned the device.
 */
class SettingsActivity : ComponentActivity() {
    private companion object {
        const val HEALTH_CONNECT_PACKAGE = "com.google.android.apps.healthdata"
        const val SAMSUNG_HEALTH_PACKAGE = "com.sec.android.app.shealth"
        const val LOCATION_STOP_ACTION = "cyou.rime.telemetry.collector.STOP_LOCATION"
    }

    private val requestedPermissions = setOf(
        HealthPermission.getReadPermission(StepsRecord::class),
        HealthPermission.getReadPermission(HeartRateRecord::class),
        HealthPermission.getReadPermission(SleepSessionRecord::class),
        HealthPermission.getReadPermission(ExerciseSessionRecord::class),
    )

    private lateinit var status: TextView
    private lateinit var collectButton: Button
    private lateinit var collectDeviceButton: Button
    private var client: HealthConnectClient? = null
    private lateinit var permissionLauncher: ActivityResultLauncher<Set<String>>
    private lateinit var locationPermissionLauncher: ActivityResultLauncher<Array<String>>
    private lateinit var backgroundLocationLauncher: ActivityResultLauncher<String>
    private lateinit var notificationSwitch: Switch
    private lateinit var deliveryUrlInput: EditText
    private lateinit var deliveryTokenInput: EditText
    private lateinit var freshnessStatus: TextView
    private lateinit var notificationPermissionLauncher: ActivityResultLauncher<String>
    private lateinit var smsPermissionLauncher: ActivityResultLauncher<String>
    private lateinit var smsStatus: TextView
    private lateinit var smsSwitch: Switch
    private lateinit var smsCollectButton: Button
    private var enableSmsAfterPermission = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        permissionLauncher = registerForActivityResult(
            PermissionController.createRequestPermissionResultContract(),
        ) { refreshPermissionStatus() }
        locationPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestMultiplePermissions(),
        ) { result ->
            status.text = if (result.values.any { it }) {
                "Location permission granted. Background access is a separate opt-in."
            } else {
                "Location permission was not granted."
            }
        }
        backgroundLocationLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { granted -> status.text = if (granted) "Background location is granted." else "Background location was not granted." }

        notificationPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { startFastCollection() }
        smsPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { granted ->
            if (granted && enableSmsAfterPermission) smsSwitch.isChecked = true
            enableSmsAfterPermission = false
            refreshSmsStatus()
        }
        val padding = (24 * resources.displayMetrics.density).toInt()
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(padding, padding, padding, padding)
        }
        content.addView(TextView(this).apply {
            text = "Telemetry collector"
            textSize = 24f
        })
        status = TextView(this).apply { textSize = 16f }
        content.addView(
            status,
            LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = padding },
        )
        addFreshnessControls(content)
        content.addView(Button(this).apply {
            text = "Check Health Connect"
            setOnClickListener { checkHealthConnect() }
        })
        content.addView(Button(this).apply {
            text = "Manage Health Connect permissions"
            setOnClickListener { openHealthConnect() }
        })
        content.addView(Button(this).apply {
            text = "Open Samsung Health"
            setOnClickListener { openSamsungHealth() }
        })
        content.addView(Button(this).apply {
            text = "Grant selected read permissions"
            setOnClickListener {
                status.text = "Opening Health Connect permission manager…"
                requestPermissions()
            }
        })
        collectButton = Button(this).apply {
            text = "Collect available health history"
            isEnabled = false
            setOnClickListener { collectHistory() }
        }
        content.addView(collectButton)
        content.addView(TextView(this).apply {
            text = "Start fresh collection for frequent updates. Android also schedules a 15-minute fallback; timing may be delayed by sleep or battery restrictions. Missing health data does not stop other sources."
        })
        content.addView(TextView(this).apply {
            text = "Device context"
            textSize = 20f
        })
        addSmsControls(content)
        content.addView(Button(this).apply {
            text = "Open Usage Access settings"
            setOnClickListener { startActivity(Intent(Settings.ACTION_USAGE_ACCESS_SETTINGS)) }
        })
        collectDeviceButton = Button(this).apply {
            text = "Collect app and device context"
            setOnClickListener { collectDeviceTelemetry() }
        }
        content.addView(collectDeviceButton)
        content.addView(Button(this).apply {
            text = "Grant foreground location"
            setOnClickListener { requestLocation() }
        })
        content.addView(Button(this).apply {
            text = "Grant background location"
            setOnClickListener { requestBackgroundLocation() }
        })
        content.addView(Button(this).apply {
            text = "Start location collection"
            setOnClickListener { startLocationCollection() }
        })
        content.addView(Button(this).apply {
            text = "Stop location collection"
            setOnClickListener { stopLocationCollection() }
        })
        content.addView(TextView(this).apply {
            text = "Notifications and blocking"
            textSize = 20f
        })
        notificationSwitch = Switch(this).apply {
            text = "Capture notifications into the local queue"
            isChecked = PolicyStore(this@SettingsActivity).captureNotifications
            setOnCheckedChangeListener { _, checked -> PolicyStore(this@SettingsActivity).captureNotifications = checked }
        }
        content.addView(notificationSwitch)
        content.addView(Button(this).apply {
            text = "Open notification access settings"
            setOnClickListener { startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)) }
        })
        content.addView(TextView(this).apply {
            text = "Blocking follows your policy: choose apps in the planner (Plan → Blocked apps). It needs the Telemetry blocking accessibility service switched on."
        })
        content.addView(Button(this).apply {
            text = "Open Accessibility settings"
            setOnClickListener { startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) }
        })
        content.addView(TextView(this).apply {
            text = "Server connection"
            textSize = 20f
        })
        content.addView(TextView(this).apply {
            text = "Device ID: ${DeviceIdentity.get(this@SettingsActivity)}"
        })
        val deliveryConfig = DeliveryConfig(this)
        content.addView(TextView(this).apply {
            text = "Sign in once in the Telemetry panel to pair this device. Tailscale keeps the server private; Android permissions remain separate."
        })
        content.addView(Button(this).apply {
            text = "Sign in with Clerk and pair this device"
            setOnClickListener { beginPairing() }
        })
        deliveryUrlInput = EditText(this).apply {
            hint = "Telemetry server URL"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setText(deliveryConfig.serverUrl)
            setSingleLine(true)
        }
        content.addView(deliveryUrlInput)
        deliveryTokenInput = EditText(this).apply {
            hint = "Device-scoped ingest token"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setSingleLine(true)
        }
        content.addView(deliveryTokenInput)
        content.addView(Button(this).apply {
            text = "Save delivery provisioning"
            setOnClickListener { saveDeliveryProvisioning() }
        })
        content.addView(Button(this).apply {
            text = "Upload pending queue"
            setOnClickListener { uploadPendingQueue() }
        })
        setContentView(ScrollView(this).apply { addView(content) })
        scheduleCollection()
        checkHealthConnect()
    }

    override fun onResume() {
        super.onResume()
        if (::status.isInitialized) {
            checkHealthConnect()
            refreshSmsStatus()
            resumePairingIfNeeded()
        }
    }

    private fun checkHealthConnect() {
        when (HealthConnectClient.getSdkStatus(this, HEALTH_CONNECT_PACKAGE)) {
            HealthConnectClient.SDK_UNAVAILABLE -> {
                client = null
                collectButton.isEnabled = false
                status.text = "Health Connect is unavailable on this device."
            }
            HealthConnectClient.SDK_UNAVAILABLE_PROVIDER_UPDATE_REQUIRED -> {
                client = null
                collectButton.isEnabled = false
                status.text = "Health Connect needs installation or an update. Other device sources can continue without it."
            }
            else -> {
                client = HealthConnectClient.getOrCreate(this, HEALTH_CONNECT_PACKAGE)
                refreshPermissionStatus()
            }
        }
    }

    private fun openHealthConnect() {
        val intent = when (HealthConnectClient.getSdkStatus(this, HEALTH_CONNECT_PACKAGE)) {
            HealthConnectClient.SDK_UNAVAILABLE_PROVIDER_UPDATE_REQUIRED -> Intent(
                Intent.ACTION_VIEW,
                Uri.parse("market://details?id=$HEALTH_CONNECT_PACKAGE"),
            )
            else -> HealthConnectClient.getHealthConnectManageDataIntent(
                this,
                packageName,
            )
        }
        try {
            startActivity(intent)
        } catch (error: Exception) {
            status.text = "Unable to open Health Connect: ${error.javaClass.simpleName}"
        }
    }

    private fun requestPermissions() {
        if (client == null) {
            checkHealthConnect()
            return
        }
        val background = if (client?.features?.getFeatureStatus(
                HealthConnectFeatures.FEATURE_READ_HEALTH_DATA_IN_BACKGROUND,
            ) == HealthConnectFeatures.FEATURE_STATUS_AVAILABLE
        ) setOf(HealthPermission.PERMISSION_READ_HEALTH_DATA_IN_BACKGROUND) else emptySet()
        permissionLauncher.launch(requestedPermissions + background)
    }

    private fun openSamsungHealth() {
        val launchIntent = packageManager.getLaunchIntentForPackage(SAMSUNG_HEALTH_PACKAGE)
        if (launchIntent == null) {
            status.text = "Samsung Health is not installed on this phone."
            return
        }
        startActivity(launchIntent)
    }

    private fun refreshPermissionStatus() {
        val activeClient = client ?: return
        lifecycleScope.launch {
            try {
                val granted = activeClient.permissionController.getGrantedPermissions()
                val missing = (requestedPermissions - granted).size
                collectButton.isEnabled = granted.intersect(requestedPermissions).isNotEmpty()
                status.text = if (missing == 0) {
                    "Selected health permissions are granted. Background health: ${if (HealthPermission.PERMISSION_READ_HEALTH_DATA_IN_BACKGROUND in granted) "granted" else "unavailable or needs consent"}."
                } else {
                    "$missing health permission(s) need consent. Granted sources can collect independently."
                }
            } catch (error: Exception) {
                status.text = "Unable to read Health Connect permissions: ${error.javaClass.simpleName}"
            }
        }
    }

    private fun collectHistory() {
        val activeClient = client ?: run {
            checkHealthConnect()
            return
        }
        collectButton.isEnabled = false
        status.text = "Reading approved Health Connect history…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                        HealthConnectCollector(
                            activeClient,
                            HealthQueue(applicationContext),
                            DeviceIdentity.get(applicationContext),
                        ).collectAll()
                }
                status.text = "Read ${result.recordsRead} source record(s); queued ${result.insertedRevisions} new revision(s); ${result.queueStats.logicalRecords} logical record(s) are waiting for provisioning."
            } catch (error: Exception) {
                status.text = "Health history collection stopped: ${error.javaClass.simpleName}"
            } finally {
                collectButton.isEnabled = true
            }
        }
    }

    private fun collectDeviceTelemetry() {
        collectDeviceButton.isEnabled = false
        status.text = "Reading app and device context…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    DeviceTelemetryCollector(
                        applicationContext,
                        HealthQueue(applicationContext),
                        DeviceIdentity.get(applicationContext),
                    ).collect()
                }
                status.text = "Read ${result.eventsRead} app events; queued ${result.insertedRevisions} context revisions. Usage: ${result.usageState}."
            } catch (error: UsageAccessRequiredException) {
                status.text = "Enable Usage Access first, then collect device context again."
            } catch (error: Exception) {
                status.text = "Device context collection stopped: ${error.javaClass.simpleName}"
            } finally {
                collectDeviceButton.isEnabled = true
            }
        }
    }

    private fun addSmsControls(content: LinearLayout) {
        content.addView(TextView(this).apply {
            text = "SMS inbox for banking activity"
            textSize = 20f
        })
        smsStatus = TextView(this)
        content.addView(smsStatus)
        smsSwitch = Switch(this).apply {
            text = "Capture incoming SMS inbox"
            isChecked = SmsCollectionSettings(this@SettingsActivity).enabled
            setOnCheckedChangeListener { _, checked ->
                val settings = SmsCollectionSettings(this@SettingsActivity)
                if (checked && !SmsCollectionSettings.hasReadPermission(this@SettingsActivity)) {
                    enableSmsAfterPermission = true
                    isChecked = false
                    smsStatus.text = "SMS read permission is required. Android will ask for it now."
                    smsPermissionLauncher.launch(Manifest.permission.READ_SMS)
                } else {
                    settings.enabled = checked
                    refreshSmsStatus()
                }
            }
        }
        content.addView(smsSwitch)
        content.addView(Button(this).apply {
            text = "Grant SMS read permission"
            setOnClickListener {
                enableSmsAfterPermission = false
                if (SmsCollectionSettings.hasReadPermission(this@SettingsActivity)) {
                    refreshSmsStatus()
                } else {
                    smsPermissionLauncher.launch(Manifest.permission.READ_SMS)
                }
            }
        })
        smsCollectButton = Button(this).apply {
            text = "Collect SMS inbox now"
            setOnClickListener { collectSms() }
        }
        content.addView(smsCollectButton)
        content.addView(TextView(this).apply {
            text = "Reads incoming text SMS rows only. It preserves sender, body, timestamps " +
                "and provider flags for later interpretation. It does not read sent messages, " +
                "MMS or RCS. Collection stays off until you enable it and grant Android SMS " +
                "read access."
        })
        refreshSmsStatus()
    }

    private fun refreshSmsStatus() {
        if (!::smsStatus.isInitialized) return
        val enabled = SmsCollectionSettings(this).enabled
        val granted = SmsCollectionSettings.hasReadPermission(this)
        smsCollectButton.isEnabled = enabled && granted
        smsStatus.text = when {
            !enabled -> "SMS inbox collection is off."
            !granted -> "SMS read permission is required before collection can start."
            else -> "SMS inbox collection is enabled. The next collection cycle will read a bounded page."
        }
    }

    private fun collectSms() {
        smsCollectButton.isEnabled = false
        smsStatus.text = "Reading approved SMS inbox rows…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    SmsCollector(
                        applicationContext,
                        HealthQueue(applicationContext),
                        DeviceIdentity.get(applicationContext),
                    ).collect()
                }
                smsStatus.text = "Read ${result.recordsRead} SMS inbox row(s); queued " +
                    "${result.insertedRevisions} new revision(s). State: ${result.state}."
            } catch (error: kotlinx.coroutines.CancellationException) {
                smsStatus.text = "SMS collection cancelled; queued records remain."
                throw error
            } catch (error: Exception) {
                smsStatus.text = "SMS collection failed; queued records remain. Error: " +
                    error.javaClass.simpleName + "."
            } finally {
                smsCollectButton.isEnabled = SmsCollectionSettings(this@SettingsActivity).enabled &&
                    SmsCollectionSettings.hasReadPermission(this@SettingsActivity)
            }
        }
    }

    private fun requestLocation() {
        locationPermissionLauncher.launch(
            arrayOf(Manifest.permission.ACCESS_COARSE_LOCATION, Manifest.permission.ACCESS_FINE_LOCATION),
        )
    }

    private fun requestBackgroundLocation() {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED
        ) {
            status.text = "Grant foreground location first."
            return
        }
        backgroundLocationLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
    }

    private fun startLocationCollection() {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED
        ) {
            status.text = "Grant foreground location first."
            return
        }
        ContextCompat.startForegroundService(
            this,
            Intent(this, TelemetryLocationService::class.java),
        )
        status.text = "Location collection started; a persistent notification is shown."
    }

    private fun stopLocationCollection() {
        startService(Intent(this, TelemetryLocationService::class.java).apply {
            action = LOCATION_STOP_ACTION
        })
        status.text = "Location collection stopped."
    }

    private fun saveDeliveryProvisioning() {
        val url = deliveryUrlInput.text.toString().trim()
        val token = deliveryTokenInput.text.toString()
        if (url.isBlank() || token.isBlank()) {
            status.text = "Enter both the Fedora URL and the device-scoped token."
            return
        }
        val config = DeliveryConfig(this)
        config.serverUrl = url
        config.saveToken(token)
        scheduleDelivery()
        deliveryTokenInput.text?.clear()
        status.text = "Delivery provisioning saved; token fingerprint ${config.tokenFingerprint()}…; background drain is scheduled."
    }

    private val pairingFlow by lazy {
        PairingFlow(this, lifecycleScope, onStatus = { status.text = it })
    }

    private fun beginPairing() {
        val serverUrl = deliveryUrlInput.text.toString().trim().ifBlank { DeliveryConfig(this).serverUrl }
        pairingFlow.start(serverUrl)
    }

    private fun resumePairingIfNeeded() = pairingFlow.resume()

    private fun scheduleDelivery() = BackgroundJobs.scheduleDelivery(this)

    private fun scheduleCollection() = BackgroundJobs.scheduleCollection(this)

    private fun uploadPendingQueue() {
        freshnessStatus.text = "Collecting available sources and delivering within resource limits…"
        lifecycleScope.launch {
            CollectionRuntime.runCycle(applicationContext, backgroundHealth = false, forceHealth = true)
            freshnessStatus.text = CollectionRuntime.latest(applicationContext)
        }
    }

    private fun addFreshnessControls(content: LinearLayout) {
        content.addView(TextView(this).apply {
            text = "Fresh data"
            textSize = 20f
        })
        freshnessStatus = TextView(this).apply { text = CollectionRuntime.latest(this@SettingsActivity) }
        content.addView(freshnessStatus)
        content.addView(Button(this).apply {
            text = "Start fresh collection"
            setOnClickListener {
                if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(
                        this@SettingsActivity, Manifest.permission.POST_NOTIFICATIONS,
                    ) != PackageManager.PERMISSION_GRANTED
                ) notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                else startFastCollection()
            }
        })
        content.addView(Button(this).apply {
            text = "Stop fresh collection"
            setOnClickListener {
                stopService(Intent(this@SettingsActivity, TelemetryCollectionService::class.java))
                freshnessStatus.text = "Fresh mode stopped. The Android-scheduled fallback remains available."
            }
        })
        content.addView(Button(this).apply {
            text = "Collect now and refresh status"
            setOnClickListener { uploadPendingQueue() }
        })
        content.addView(TextView(this).apply {
            text = "Health records appear after their provider writes them to Health Connect. A phone with no health records still collects its other approved sources. GPS and notification access remain separate controls below."
        })
        val store = CollectionSettingsStore(this)
        val current = store.current
        fun number(label: String, value: Int): EditText {
            content.addView(TextView(this).apply { text = label })
            return EditText(this).apply {
                inputType = InputType.TYPE_CLASS_NUMBER
                setText(value.toString())
                content.addView(this)
            }
        }
        val cycle = number("Collection target, seconds (30–300)", current.cycleSeconds)
        val health = number("Health check, minutes (1–30)", current.healthMinutes)
        val metered = number("Metered upload per cycle, KiB (16–1024)", current.meteredKiB)
        val daily = number("Metered upload per UTC day, MiB (1–1024)", current.meteredDailyMiB)
        val battery = number("Slow down below battery % (5–50)", current.batteryFloorPercent)
        val heat = number("Slow down at battery temperature C (35–50)", current.hotTemperatureC)
        content.addView(TextView(this).apply {
            text = "Limits count attempted upload payloads, including retries. Headers, downloads and VPN overhead add traffic; app TX/RX counters are shown separately. Heat, low battery or Battery Saver slows collection to five minutes."
        })
        content.addView(Button(this).apply {
            text = "Save collection settings"
            setOnClickListener {
                val values = listOf(cycle, health, metered, daily, battery, heat).map { it.text.toString().toIntOrNull() }
                if (values.any { it == null }) {
                    freshnessStatus.text = "Enter a whole number for each setting."
                } else {
                    val saved = store.save(CollectionSettings(values[0]!!, values[1]!!, values[2]!!,
                        values[3]!!, values[4]!!, values[5]!!))
                    listOf(cycle, health, metered, daily, battery, heat).zip(listOf(saved.cycleSeconds,
                        saved.healthMinutes, saved.meteredKiB, saved.meteredDailyMiB,
                        saved.batteryFloorPercent, saved.hotTemperatureC)).forEach { (input, value) -> input.setText(value.toString()) }
                    freshnessStatus.text = "Saved. Settings take effect on the next collection cycle."
                }
            }
        })
    }

    private fun startFastCollection() {
        try {
            ContextCompat.startForegroundService(this, Intent(this, TelemetryCollectionService::class.java))
            freshnessStatus.text = "Fresh collection starting. Use refresh status to inspect the next cycle."
        } catch (error: Exception) {
            freshnessStatus.text = "Could not start collection: ${error.javaClass.simpleName}"
        }
    }

}
