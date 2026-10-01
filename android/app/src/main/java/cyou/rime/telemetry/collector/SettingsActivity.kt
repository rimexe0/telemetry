package cyou.rime.telemetry.collector

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color as AndroidColor
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.health.connect.client.HealthConnectClient
import androidx.health.connect.client.HealthConnectFeatures
import androidx.health.connect.client.PermissionController
import androidx.health.connect.client.permission.HealthPermission
import androidx.health.connect.client.records.ExerciseSessionRecord
import androidx.health.connect.client.records.HeartRateRecord
import androidx.health.connect.client.records.SleepSessionRecord
import androidx.health.connect.client.records.StepsRecord
import androidx.lifecycle.lifecycleScope
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Owner-operated settings for this phone's collection, blocking and server
 * connection. It requests only the records the collector can preserve and
 * never reads or uploads health data until the owner has granted consent and
 * provisioned the device.
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

    private var client: HealthConnectClient? = null
    private lateinit var permissionLauncher: ActivityResultLauncher<Set<String>>
    private lateinit var locationPermissionLauncher: ActivityResultLauncher<Array<String>>
    private lateinit var backgroundLocationLauncher: ActivityResultLauncher<String>
    private lateinit var notificationPermissionLauncher: ActivityResultLauncher<String>
    private lateinit var smsPermissionLauncher: ActivityResultLauncher<String>
    private var enableSmsAfterPermission = false

    // Short notices go to a snackbar; lasting states are shown in their section.
    private val notices = SnackbarHostState()
    private var healthStatus by mutableStateOf("")
    private var healthGranted by mutableStateOf(false)
    private var healthBusy by mutableStateOf(false)
    private var deviceBusy by mutableStateOf(false)
    private var freshnessStatus by mutableStateOf("")
    private var smsStatus by mutableStateOf("")
    private var smsEnabled by mutableStateOf(false)
    private var smsReady by mutableStateOf(false)
    private var smsBusy by mutableStateOf(false)
    private var captureNotifications by mutableStateOf(false)
    private var suspendEnabled by mutableStateOf(false)
    private var suspendStatus by mutableStateOf("")
    private var blockingOn by mutableStateOf(false)
    private var paired by mutableStateOf(false)
    private var pairingStatus by mutableStateOf("")
    private var serverUrl by mutableStateOf("")
    private var serverToken by mutableStateOf("")
    private var limits by mutableStateOf(listOf<String>())
    private var advancedOpen by mutableStateOf(false)
    private var statusOpen by mutableStateOf(false)

    private fun notice(text: String) {
        lifecycleScope.launch {
            notices.currentSnackbarData?.dismiss()
            notices.showSnackbar(text)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(AndroidColor.TRANSPARENT), SystemBarStyle.dark(AndroidColor.TRANSPARENT))
        super.onCreate(savedInstanceState)
        permissionLauncher = registerForActivityResult(
            PermissionController.createRequestPermissionResultContract(),
        ) { refreshPermissionStatus() }
        locationPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestMultiplePermissions(),
        ) { result ->
            notice(if (result.values.any { it }) {
                "Location permission granted. Background access is a separate opt-in."
            } else {
                "Location permission was not granted."
            })
        }
        backgroundLocationLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { granted -> notice(if (granted) "Background location is granted." else "Background location was not granted.") }
        notificationPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { startFastCollection() }
        smsPermissionLauncher = registerForActivityResult(
            ActivityResultContracts.RequestPermission(),
        ) { granted ->
            if (granted && enableSmsAfterPermission) SmsCollectionSettings(this).enabled = true
            enableSmsAfterPermission = false
            refreshSmsStatus()
        }

        val deliveryConfig = DeliveryConfig(this)
        serverUrl = deliveryConfig.serverUrl
        freshnessStatus = CollectionRuntime.latest(this)
        captureNotifications = PolicyStore(this).captureNotifications
        suspendEnabled = AppSuspender.enabled(this)
        suspendStatus = AppSuspender.lastError(this).orEmpty()
        val current = CollectionSettingsStore(this).current
        limits = listOf(current.cycleSeconds, current.healthMinutes, current.meteredKiB, current.meteredDailyMiB,
            current.batteryFloorPercent, current.hotTemperatureC).map { it.toString() }

        setContent { TelemetryTheme { Screen() } }
        scheduleCollection()
        checkHealthConnect()
    }

    override fun onResume() {
        super.onResume()
        checkHealthConnect()
        refreshSmsStatus()
        resumePairingIfNeeded()
        blockingOn = PolicyEvents.blockingEnabled(this)
        paired = DeliveryConfig(this).token() != null
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    private fun Screen() {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("Settings") },
                    navigationIcon = { IconButton(onClick = ::finish) { Icon(painterResource(R.drawable.ic_back), contentDescription = "Back") } },
                )
            },
            snackbarHost = { SnackbarHost(notices) },
        ) { padding ->
            Column(
                modifier = Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp).padding(bottom = 24.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Section("Connection") {
                    Item(if (paired) "This phone is paired" else "This phone is not paired", "Device ID: ${DeviceIdentity.get(this@SettingsActivity)}")
                    Item("Pair this device", pairingStatus.ifBlank { "Sign in once in the Telemetry panel. Tailscale keeps the server private." }, onClick = ::beginPairing)
                }
                Section("Blocking") {
                    Item("Blocking service", if (blockingOn) "On. Blocked apps are closed during blocks." else "Off. Tap to switch on Telemetry blocking in Accessibility.",
                        onClick = { startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) })
                    Toggle("Gray out blocked apps", suspendStatus.ifBlank { "Needs root. The block screen stays as the fallback." }, suspendEnabled, ::changeSuspension)
                    Note("Blocking follows your policy. Tell the assistant what is a distraction, or choose apps in the planner.")
                }
                Section("Collection") {
                    // The last cycle's report is long; its first lines say whether it ran and uploaded.
                    val lines = freshnessStatus.lines()
                    Note(if (statusOpen) freshnessStatus else lines.take(4).joinToString("\n"))
                    if (lines.size > 4) Item(if (statusOpen) "Hide the full report" else "Show the full report", onClick = { statusOpen = !statusOpen })
                    Item("Start fresh collection", "Frequent updates with a visible notification.", onClick = ::requestFastCollection)
                    Item("Stop fresh collection", "Android's 15-minute fallback keeps running.", onClick = {
                        stopService(Intent(this@SettingsActivity, TelemetryCollectionService::class.java))
                        freshnessStatus = "Fresh mode stopped. The Android-scheduled fallback remains available."
                    })
                    Item("Collect now", "Reads every available source and uploads.", onClick = ::uploadPendingQueue)
                    Item("Usage access", "Lets Telemetry see which app is open.", onClick = { startActivity(Intent(Settings.ACTION_USAGE_ACCESS_SETTINGS)) })
                    Item("Collect app and device context", enabled = !deviceBusy, onClick = ::collectDeviceTelemetry)
                }
                Section("Notifications") {
                    Toggle("Capture notifications", "Kept in the local queue until uploaded.", captureNotifications) { checked ->
                        PolicyStore(this@SettingsActivity).captureNotifications = checked
                        captureNotifications = checked
                    }
                    Item("Notification access", "Android's setting for reading notifications.", onClick = { startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)) })
                }
                Section("Health") {
                    Note(healthStatus)
                    Item("Check Health Connect", onClick = ::checkHealthConnect)
                    Item("Grant health permissions", "Steps, heart rate, sleep and exercise.", onClick = ::requestPermissions)
                    Item("Manage Health Connect", onClick = ::openHealthConnect)
                    Item("Open Samsung Health", onClick = ::openSamsungHealth)
                    Item("Collect health history", enabled = healthGranted && !healthBusy, onClick = ::collectHistory)
                    Note("Health records appear after their provider writes them to Health Connect. Missing health data does not stop other sources.")
                }
                Section("SMS") {
                    Toggle("Capture incoming SMS", smsStatus, smsEnabled, ::changeSms)
                    Item("Grant SMS permission", onClick = {
                        enableSmsAfterPermission = false
                        if (SmsCollectionSettings.hasReadPermission(this@SettingsActivity)) refreshSmsStatus()
                        else smsPermissionLauncher.launch(Manifest.permission.READ_SMS)
                    })
                    Item("Collect SMS now", enabled = smsReady && !smsBusy, onClick = ::collectSms)
                    Note("Reads incoming text SMS only, for banking activity: sender, body, timestamps and provider flags. Not sent messages, MMS or RCS.")
                }
                Section("Location") {
                    Item("Grant location", onClick = ::requestLocation)
                    Item("Grant location all the time", onClick = ::requestBackgroundLocation)
                    Item("Start location collection", "Shows a persistent notification.", onClick = ::startLocationCollection)
                    Item("Stop location collection", onClick = ::stopLocationCollection)
                }
                Section("Advanced") {
                    Item(if (advancedOpen) "Hide limits and manual setup" else "Show limits and manual setup", onClick = { advancedOpen = !advancedOpen })
                    if (advancedOpen) {
                        val labels = listOf("Collection target, seconds (30–300)", "Health check, minutes (1–30)", "Metered upload per cycle, KiB (16–1024)",
                            "Metered upload per UTC day, MiB (1–1024)", "Slow down below battery % (5–50)", "Slow down at battery temperature °C (35–50)")
                        labels.forEachIndexed { index, label ->
                            Field(label, limits[index], number = true) { value -> limits = limits.toMutableList().also { it[index] = value } }
                        }
                        Note("Limits count attempted upload payloads, including retries. Heat, low battery or Battery Saver slows collection to five minutes.")
                        Item("Save collection limits", onClick = ::saveLimits)
                        Field("Telemetry server URL", serverUrl) { serverUrl = it }
                        Field("Device-scoped ingest token", serverToken, secret = true) { serverToken = it }
                        Item("Save manual server setup", onClick = ::saveDeliveryProvisioning)
                    }
                }
            }
        }
    }

    @Composable
    private fun Section(title: String, content: @Composable ColumnScope.() -> Unit) {
        Text(title.uppercase(), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(start = 4.dp, top = 12.dp))
        Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer), modifier = Modifier.fillMaxWidth(), content = content)
    }

    @Composable
    private fun Item(title: String, detail: String? = null, enabled: Boolean = true, onClick: (() -> Unit)? = null) {
        val faded = if (enabled) 1f else 0.4f
        ListItem(
            headlineContent = { Text(title, color = MaterialTheme.colorScheme.onSurface.copy(alpha = faded)) },
            supportingContent = detail?.takeIf { it.isNotBlank() }?.let { { Text(it) } },
            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
            modifier = if (onClick != null) Modifier.clickable(enabled = enabled, onClick = onClick) else Modifier,
        )
    }

    @Composable
    private fun Toggle(title: String, detail: String, checked: Boolean, onChange: (Boolean) -> Unit) {
        ListItem(
            headlineContent = { Text(title) },
            supportingContent = detail.takeIf { it.isNotBlank() }?.let { { Text(it) } },
            trailingContent = { Switch(checked = checked, onCheckedChange = onChange) },
            colors = ListItemDefaults.colors(containerColor = Color.Transparent),
            modifier = Modifier.clickable { onChange(!checked) },
        )
    }

    @Composable
    private fun Note(text: String) {
        if (text.isBlank()) return
        Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
    }

    @Composable
    private fun Field(label: String, value: String, number: Boolean = false, secret: Boolean = false, onChange: (String) -> Unit) {
        OutlinedTextField(
            value = value,
            onValueChange = onChange,
            label = { Text(label) },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = if (number) KeyboardType.Number else if (secret) KeyboardType.Password else KeyboardType.Uri),
            visualTransformation = if (secret) PasswordVisualTransformation() else androidx.compose.ui.text.input.VisualTransformation.None,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        )
    }

    private fun changeSuspension(checked: Boolean) {
        // Root may ask for permission, so this runs off the main thread.
        Thread {
            val ok = AppSuspender.setEnabled(this, checked)
            runOnUiThread {
                suspendEnabled = checked && ok
                suspendStatus = AppSuspender.lastError(this)
                    ?: if (checked) "Blocked apps are suspended during blocks; the block screen stays as the fallback." else "Suspension is off; the block screen applies."
            }
        }.start()
    }

    private fun changeSms(checked: Boolean) {
        if (checked && !SmsCollectionSettings.hasReadPermission(this)) {
            enableSmsAfterPermission = true
            smsStatus = "SMS read permission is required. Android will ask for it now."
            smsPermissionLauncher.launch(Manifest.permission.READ_SMS)
        } else {
            SmsCollectionSettings(this).enabled = checked
            refreshSmsStatus()
        }
    }

    private fun requestFastCollection() {
        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            startFastCollection()
        }
    }

    private fun saveLimits() {
        val values = limits.map { it.toIntOrNull() }
        if (values.any { it == null }) {
            notice("Enter a whole number for each setting.")
            return
        }
        val saved = CollectionSettingsStore(this).save(CollectionSettings(values[0]!!, values[1]!!, values[2]!!, values[3]!!, values[4]!!, values[5]!!))
        limits = listOf(saved.cycleSeconds, saved.healthMinutes, saved.meteredKiB, saved.meteredDailyMiB, saved.batteryFloorPercent, saved.hotTemperatureC).map { it.toString() }
        notice("Saved. Settings take effect on the next collection cycle.")
    }

    private fun checkHealthConnect() {
        when (HealthConnectClient.getSdkStatus(this, HEALTH_CONNECT_PACKAGE)) {
            HealthConnectClient.SDK_UNAVAILABLE -> {
                client = null
                healthGranted = false
                healthStatus = "Health Connect is unavailable on this device."
            }
            HealthConnectClient.SDK_UNAVAILABLE_PROVIDER_UPDATE_REQUIRED -> {
                client = null
                healthGranted = false
                healthStatus = "Health Connect needs installation or an update. Other device sources can continue without it."
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
            healthStatus = "Unable to open Health Connect: ${error.javaClass.simpleName}"
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
            healthStatus = "Samsung Health is not installed on this phone."
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
                healthGranted = granted.intersect(requestedPermissions).isNotEmpty()
                healthStatus = if (missing == 0) {
                    "Selected health permissions are granted. Background health: ${if (HealthPermission.PERMISSION_READ_HEALTH_DATA_IN_BACKGROUND in granted) "granted" else "unavailable or needs consent"}."
                } else {
                    "$missing health permission(s) need consent. Granted sources can collect independently."
                }
            } catch (error: Exception) {
                healthStatus = "Unable to read Health Connect permissions: ${error.javaClass.simpleName}"
            }
        }
    }

    private fun collectHistory() {
        val activeClient = client ?: run {
            checkHealthConnect()
            return
        }
        healthBusy = true
        healthStatus = "Reading approved Health Connect history…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                        HealthConnectCollector(
                            activeClient,
                            HealthQueue(applicationContext),
                            DeviceIdentity.get(applicationContext),
                        ).collectAll()
                }
                healthStatus = "Read ${result.recordsRead} source record(s); queued ${result.insertedRevisions} new revision(s); ${result.queueStats.logicalRecords} logical record(s) are waiting for provisioning."
            } catch (error: Exception) {
                healthStatus = "Health history collection stopped: ${error.javaClass.simpleName}"
            } finally {
                healthBusy = false
            }
        }
    }

    private fun collectDeviceTelemetry() {
        deviceBusy = true
        notice("Reading app and device context…")
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    DeviceTelemetryCollector(
                        applicationContext,
                        HealthQueue(applicationContext),
                        DeviceIdentity.get(applicationContext),
                    ).collect()
                }
                notice("Read ${result.eventsRead} app events; queued ${result.insertedRevisions} context revisions. Usage: ${result.usageState}.")
            } catch (error: UsageAccessRequiredException) {
                notice("Enable Usage Access first, then collect device context again.")
            } catch (error: Exception) {
                notice("Device context collection stopped: ${error.javaClass.simpleName}")
            } finally {
                deviceBusy = false
            }
        }
    }

    private fun refreshSmsStatus() {
        val enabled = SmsCollectionSettings(this).enabled
        val granted = SmsCollectionSettings.hasReadPermission(this)
        smsEnabled = enabled
        smsReady = enabled && granted
        smsStatus = when {
            !enabled -> "SMS inbox collection is off."
            !granted -> "SMS read permission is required before collection can start."
            else -> "SMS inbox collection is enabled. The next collection cycle will read a bounded page."
        }
    }

    private fun collectSms() {
        smsBusy = true
        smsStatus = "Reading approved SMS inbox rows…"
        lifecycleScope.launch {
            try {
                val result = withContext(Dispatchers.IO) {
                    SmsCollector(
                        applicationContext,
                        HealthQueue(applicationContext),
                        DeviceIdentity.get(applicationContext),
                    ).collect()
                }
                smsStatus = "Read ${result.recordsRead} SMS inbox row(s); queued " +
                    "${result.insertedRevisions} new revision(s). State: ${result.state}."
            } catch (error: kotlinx.coroutines.CancellationException) {
                smsStatus = "SMS collection cancelled; queued records remain."
                throw error
            } catch (error: Exception) {
                smsStatus = "SMS collection failed; queued records remain. Error: " +
                    error.javaClass.simpleName + "."
            } finally {
                smsBusy = false
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
            notice("Grant foreground location first.")
            return
        }
        backgroundLocationLauncher.launch(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
    }

    private fun startLocationCollection() {
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) != PackageManager.PERMISSION_GRANTED
        ) {
            notice("Grant foreground location first.")
            return
        }
        ContextCompat.startForegroundService(
            this,
            Intent(this, TelemetryLocationService::class.java),
        )
        notice("Location collection started; a persistent notification is shown.")
    }

    private fun stopLocationCollection() {
        startService(Intent(this, TelemetryLocationService::class.java).apply {
            action = LOCATION_STOP_ACTION
        })
        notice("Location collection stopped.")
    }

    private fun saveDeliveryProvisioning() {
        val url = serverUrl.trim()
        val token = serverToken
        if (url.isBlank() || token.isBlank()) {
            notice("Enter both the server URL and the device-scoped token.")
            return
        }
        val config = DeliveryConfig(this)
        config.serverUrl = url
        config.saveToken(token)
        scheduleDelivery()
        serverToken = ""
        paired = true
        notice("Delivery provisioning saved; token fingerprint ${config.tokenFingerprint()}…; background drain is scheduled.")
    }

    private val pairingFlow by lazy {
        PairingFlow(this, lifecycleScope, onStatus = { pairingStatus = it })
    }

    private fun beginPairing() {
        pairingFlow.start(serverUrl.trim().ifBlank { DeliveryConfig(this).serverUrl })
    }

    private fun resumePairingIfNeeded() = pairingFlow.resume()

    private fun scheduleDelivery() = BackgroundJobs.scheduleDelivery(this)

    private fun scheduleCollection() = BackgroundJobs.scheduleCollection(this)

    private fun uploadPendingQueue() {
        freshnessStatus = "Collecting available sources and delivering within resource limits…"
        lifecycleScope.launch {
            CollectionRuntime.runCycle(applicationContext, backgroundHealth = false, forceHealth = true)
            freshnessStatus = CollectionRuntime.latest(applicationContext)
        }
    }

    private fun startFastCollection() {
        try {
            ContextCompat.startForegroundService(this, Intent(this, TelemetryCollectionService::class.java))
            freshnessStatus = "Fresh collection starting. Use refresh status to inspect the next cycle."
        } catch (error: Exception) {
            freshnessStatus = "Could not start collection: ${error.javaClass.simpleName}"
        }
    }

}
