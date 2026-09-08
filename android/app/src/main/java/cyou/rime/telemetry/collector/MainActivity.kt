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
class MainActivity : ComponentActivity() {
    private companion object {
        const val HEALTH_CONNECT_PACKAGE = "com.google.android.apps.healthdata"
        const val SAMSUNG_HEALTH_PACKAGE = "com.sec.android.app.shealth"
        const val LOCATION_STOP_ACTION = "cyou.rime.telemetry.collector.STOP_LOCATION"
        const val COLLECTION_WORK_NAME = "telemetry-collection"
        const val DELIVERY_WORK_NAME = "telemetry-delivery"
        const val PAIRING_POLL_INTERVAL_MS = 3_000L
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
    private lateinit var blockedPackagesInput: EditText
    private lateinit var softBlockSwitch: Switch
    private lateinit var notificationSwitch: Switch
    private lateinit var deliveryUrlInput: EditText
    private lateinit var deliveryTokenInput: EditText
    private var pairingJob: Job? = null

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

        val padding = (24 * resources.displayMetrics.density).toInt()
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(padding, padding, padding, padding)
        }
        content.addView(TextView(this).apply {
            text = "Telemetry health collector"
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
            text = "Collection runs every 15 minutes when permissions are available. Records stay in the app-private queue until delivery succeeds."
        })
        content.addView(TextView(this).apply {
            text = "Device context"
            textSize = 20f
        })
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
            text = "Notifications and soft block"
            textSize = 20f
        })
        notificationSwitch = Switch(this).apply {
            text = "Capture notifications into the local queue"
            isChecked = PolicyStore(this@MainActivity).captureNotifications
            setOnCheckedChangeListener { _, checked -> PolicyStore(this@MainActivity).captureNotifications = checked }
        }
        content.addView(notificationSwitch)
        content.addView(Button(this).apply {
            text = "Open notification access settings"
            setOnClickListener { startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)) }
        })
        softBlockSwitch = Switch(this).apply {
            text = "Enable configured soft block"
            isChecked = PolicyStore(this@MainActivity).softBlockEnabled
        }
        content.addView(softBlockSwitch)
        blockedPackagesInput = EditText(this).apply {
            hint = "Blocked package names, comma separated"
            setText(PolicyStore(this@MainActivity).blockedPackages.sorted().joinToString(","))
            setSingleLine(false)
        }
        content.addView(blockedPackagesInput)
        content.addView(Button(this).apply {
            text = "Save soft-block policy"
            setOnClickListener { saveSoftBlockPolicy() }
        })
        content.addView(Button(this).apply {
            text = "Open Accessibility settings"
            setOnClickListener { startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) }
        })
        content.addView(TextView(this).apply {
            text = "Fedora delivery"
            textSize = 20f
        })
        content.addView(TextView(this).apply {
            text = "Device ID: ${DeviceIdentity.get(this@MainActivity)}"
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
                status.text = "Install or update Health Connect, then enable Samsung Health sharing."
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
        permissionLauncher.launch(requestedPermissions)
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
                collectButton.isEnabled = missing == 0
                status.text = if (missing == 0) {
                    "Selected permissions are granted. The local queue is ready."
                } else {
                    "$missing selected permission(s) still need consent."
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
                status.text = "Read ${result.eventsRead} app events; queued ${result.insertedRevisions} new context revision(s)."
            } catch (error: UsageAccessRequiredException) {
                status.text = "Enable Usage Access first, then collect device context again."
            } catch (error: Exception) {
                status.text = "Device context collection stopped: ${error.javaClass.simpleName}"
            } finally {
                collectDeviceButton.isEnabled = true
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

    private fun saveSoftBlockPolicy() {
        val packages = blockedPackagesInput.text.toString()
            .split(',')
            .map(String::trim)
            .filter(String::isNotBlank)
            .toSet()
        val policy = PolicyStore(this)
        policy.blockedPackages = packages
        policy.softBlockEnabled = softBlockSwitch.isChecked && packages.isNotEmpty()
        status.text = if (policy.softBlockEnabled) {
            "Soft block saved for ${packages.size} package(s). Enable the Telemetry accessibility service to enforce it."
        } else {
            "Soft block disabled; no packages are blocked."
        }
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

    private fun beginPairing() {
        val config = DeliveryConfig(this)
        val serverUrl = deliveryUrlInput.text.toString().trim().ifBlank { config.serverUrl }
        if (serverUrl.isBlank()) {
            status.text = "Enter the private Telemetry server URL first."
            return
        }
        pairingJob?.cancel()
        val secret = try { config.newPairingSecret(serverUrl) } catch (error: Exception) {
            status.text = "Finish the pending connection before changing servers."
            return
        }
        status.text = "Creating a secure pairing request…"
        lifecycleScope.launch {
            try {
                val retainedId = config.pairingId
                val start = if (retainedId != null) {
                    PairingStart(retainedId, Uri.parse(serverUrl.trimEnd('/') + "/panel/").buildUpon().appendQueryParameter("pair", retainedId).build().toString())
                } else withContext(Dispatchers.IO) {
                    TelemetryPairingClient(
                        serverUrl = serverUrl,
                        deviceId = DeviceIdentity.get(applicationContext),
                        deviceName = deviceDisplayName(),
                    ).start(secret)
                }
                config.savePairing(start.id, secret)
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(start.panelUrl)))
                status.text = "Telemetry panel opened. Sign in there to approve this device."
                pollPairing(serverUrl, start.id, secret)
            } catch (error: Exception) {
                if (error is kotlinx.coroutines.CancellationException) throw error
                status.text = "Unable to start pairing; queued records remain on this device."
            }
        }
    }

    private fun resumePairingIfNeeded() {
        val config = DeliveryConfig(this)
        val pairingId = config.pairingId ?: return
        val secret = config.pairingSecret() ?: return
        if (pairingJob?.isActive == true) return
        pollPairing(config.pairingServer() ?: return, pairingId, secret)
    }

    private fun pollPairing(serverUrl: String, pairingId: String, secret: String) {
        pairingJob?.cancel()
        pairingJob = lifecycleScope.launch {
            val config = DeliveryConfig(applicationContext)
            val client = TelemetryPairingClient(
                serverUrl = serverUrl,
                deviceId = DeviceIdentity.get(applicationContext),
                deviceName = deviceDisplayName(),
            )
            while (true) {
                try {
                    val result = withContext(Dispatchers.IO) { client.poll(pairingId, secret) }
                    when (result.state) {
                        PairingStatus.State.PENDING -> {
                            status.text = "Waiting for approval in the Telemetry panel…"
                        }
                        PairingStatus.State.APPROVED -> {
                            val returnedDeviceId = result.deviceId
                            if (returnedDeviceId != DeviceIdentity.get(applicationContext)) {
                                throw IllegalStateException("pairing approval was for another device")
                            }
                            config.completePairing(serverUrl, secret)
                            scheduleDelivery()
                            status.text = "Device paired. Background delivery is scheduled."
                            return@launch
                        }
                        PairingStatus.State.DENIED -> {
                            config.clearPairing()
                            status.text = "Pairing was denied. Queued records remain on this device."
                            return@launch
                        }
                        PairingStatus.State.EXPIRED -> {
                            config.clearPairing()
                            status.text = "Pairing expired. Start it again when ready."
                            return@launch
                        }
                    }
                } catch (error: PairingException) {
                    if (error.status !in setOf(408, 425, 429, 500, 502, 503, 504)) {
                        config.clearPairing()
                        status.text = "Pairing could not be continued; start it again when ready."
                        return@launch
                    }
                    status.text = "Waiting for the private Telemetry server…"
                } catch (error: IllegalStateException) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    status.text = "Pairing response was invalid; start it again when ready."
                    return@launch
                } catch (error: Exception) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    status.text = "Waiting for the private Telemetry server…"
                }
                delay(PAIRING_POLL_INTERVAL_MS)
            }
        }
    }

    private fun deviceDisplayName(): String = listOf(Build.MANUFACTURER, Build.MODEL)
        .filter(String::isNotBlank)
        .joinToString(" ")
        .ifBlank { "Android device" }

    private fun scheduleDelivery() {
        scheduleCollection()
        WorkManager.getInstance(this).enqueueUniquePeriodicWork(
            DELIVERY_WORK_NAME,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<TelemetryUploadWorker>(15, TimeUnit.MINUTES)
                .setConstraints(
                    Constraints.Builder()
                        .setRequiredNetworkType(NetworkType.CONNECTED)
                        .build(),
                )
                .build(),
        )
    }

    private fun scheduleCollection() {
        WorkManager.getInstance(this).enqueueUniquePeriodicWork(
            COLLECTION_WORK_NAME,
            ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<TelemetryCollectionWorker>(15, TimeUnit.MINUTES)
                .build(),
        )
    }

    private fun uploadPendingQueue() {
        val config = DeliveryConfig(this)
        val url = config.serverUrl
        val token = runCatching { config.token() }.getOrNull()
        if (url.isBlank() || token.isNullOrBlank()) {
            status.text = "Save delivery provisioning first."
            return
        }
        status.text = "Uploading one bounded queue batch…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    TelemetryDeliveryClient(
                        HealthQueue(applicationContext),
                        DeviceIdentity.get(applicationContext),
                        url,
                        token,
                    ).drainOnce()
                }
                status.text = if (result.claimed == 0) {
                    "The local queue is empty."
                } else {
                    "Acknowledged ${result.acknowledged}/${result.claimed} queued revision(s)."
                }
            } catch (error: Exception) {
                status.text = if (error is DeliveryException) {
                    "Delivery rejected with HTTP ${error.status}; queued records remain."
                } else {
                    "Delivery stopped; queued records remain: ${error.javaClass.simpleName}"
                }
            }
        }
    }

}
