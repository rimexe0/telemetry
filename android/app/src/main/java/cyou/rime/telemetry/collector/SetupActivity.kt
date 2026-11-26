package cyou.rime.telemetry.collector

import android.Manifest
import android.app.AppOpsManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.os.Process
import android.provider.Settings
import android.text.InputType
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.app.NotificationManagerCompat
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
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Guided setup: asks for one thing at a time and moves on by itself. */
class SetupActivity : ComponentActivity() {
    companion object {
        private const val PREFS = "telemetry-setup"
        private const val KEY_SKIPPED = "skipped"
        /** Opens setup at this step (a [SetupStep] name), even one skipped before. */
        const val EXTRA_STEP = "step"

        fun intent(context: Context, step: SetupStep? = null) = Intent(context, SetupActivity::class.java).apply {
            step?.let { putExtra(EXTRA_STEP, it.name) }
        }
        const val KEY_STARTED = "collection_started"
        private const val HEALTH_CONNECT_PACKAGE = "com.google.android.apps.healthdata"
        private val BACKGROUND = Color.rgb(17, 18, 19)
        private val FOREGROUND = Color.rgb(238, 238, 235)
        private val MUTED = Color.rgb(170, 169, 164)

        private val healthPermissions = setOf(
            HealthPermission.getReadPermission(StepsRecord::class),
            HealthPermission.getReadPermission(HeartRateRecord::class),
            HealthPermission.getReadPermission(SleepSessionRecord::class),
            HealthPermission.getReadPermission(ExerciseSessionRecord::class),
        )

        /** Steps that can be checked without asynchronous calls. */
        fun syncDone(context: Context): Set<SetupStep> {
            val done = mutableSetOf<SetupStep>()
            if (DeliveryConfig(context).token() != null) done += SetupStep.PAIR
            if (Build.VERSION.SDK_INT < 33 || granted(context, Manifest.permission.POST_NOTIFICATIONS)) done += SetupStep.NOTIFICATIONS
            if (usageAccess(context)) done += SetupStep.USAGE_ACCESS
            if (granted(context, Manifest.permission.ACCESS_FINE_LOCATION) || granted(context, Manifest.permission.ACCESS_COARSE_LOCATION)) done += SetupStep.LOCATION
            if (granted(context, Manifest.permission.ACCESS_BACKGROUND_LOCATION)) done += SetupStep.BACKGROUND_LOCATION
            if (granted(context, Manifest.permission.READ_CALENDAR)) done += SetupStep.CALENDAR
            // Only asked where Samsung Health is installed and the reader is built in.
            if (!SamsungHealthSource.SUPPORTED || !SamsungHealthSource.installed(context) || SamsungHealthSource.grantedLastTime(context)) done += SetupStep.SAMSUNG_HEALTH
            if (context.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(context.packageName)) done += SetupStep.BATTERY
            if (PolicyEvents.blockingEnabled(context)) done += SetupStep.BLOCKING
            if (NotificationManagerCompat.getEnabledListenerPackages(context).contains(context.packageName)) done += SetupStep.NOTIFICATION_ACCESS
            if (preferences(context).getBoolean(KEY_STARTED, false)) done += SetupStep.START
            return done
        }

        /** True when a required step is still missing; the app then opens setup first. */
        fun needsSetup(context: Context): Boolean = SetupStep.remainingRequired(syncDone(context) + SetupStep.HEALTH) > 0

        private fun preferences(context: Context) = context.getSharedPreferences(PREFS, MODE_PRIVATE)

        private fun granted(context: Context, permission: String) =
            ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED

        private fun usageAccess(context: Context): Boolean {
            val appOps = context.getSystemService(AppOpsManager::class.java)
            return appOps.unsafeCheckOpNoThrow(AppOpsManager.OPSTR_GET_USAGE_STATS, Process.myUid(), context.packageName) == AppOpsManager.MODE_ALLOWED
        }
    }

    private lateinit var progress: TextView
    private lateinit var title: TextView
    private lateinit var why: TextView
    private lateinit var status: TextView
    private lateinit var server: EditText
    private lateinit var code: EditText
    private lateinit var scan: Button
    private var codeTried = ""
    private lateinit var primary: Button
    private lateinit var skip: Button
    private lateinit var approveHere: Button
    private lateinit var permissionLauncher: ActivityResultLauncher<Array<String>>
    private lateinit var healthLauncher: ActivityResultLauncher<Set<String>>
    private var current: SetupStep? = null
    private var healthDone = false
    private val pairingFlow: PairingFlow by lazy {
        PairingFlow(this, lifecycleScope, onStatus = {
            status.text = it
            approveHere.visibility = if (current == SetupStep.PAIR && pairingFlow.approvalUrl != null) View.VISIBLE else View.GONE
        }, onPaired = { refresh() })
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        permissionLauncher = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { results ->
            // Refusing notifications is an answer; they are not asked for again.
            if (current == SetupStep.NOTIFICATIONS && results.values.any { !it }) skipStep(SetupStep.NOTIFICATIONS) else refresh()
        }
        healthLauncher = registerForActivityResult(PermissionController.createRequestPermissionResultContract()) { refresh() }
        val density = resources.displayMetrics.density
        val pad = (24 * density).toInt()
        fun text(size: Float, color: Int = FOREGROUND, bold: Boolean = false) = TextView(this).apply {
            textSize = size
            setTextColor(color)
            if (bold) typeface = Typeface.DEFAULT_BOLD
            setPadding(0, (8 * density).toInt(), 0, (8 * density).toInt())
        }
        progress = text(14f, MUTED, bold = true).apply { letterSpacing = 0.1f }
        title = text(28f, bold = true)
        why = text(17f)
        status = text(15f, MUTED)
        // A public build has no server built in: its address is asked here,
        // unless a setup link or an earlier build already gave it.
        server = EditText(this).apply {
            hint = "https://server.your-tailnet.ts.net:8443"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine()
            setTextColor(FOREGROUND)
            setHintTextColor(MUTED)
            visibility = View.GONE
        }
        // The one-time code from the panel's Settings → Devices → Add a device.
        code = EditText(this).apply {
            hint = "Setup code, like K7QM-3XPD (optional)"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            setSingleLine()
            setTextColor(FOREGROUND)
            setHintTextColor(MUTED)
            visibility = View.GONE
        }
        primary = Button(this).apply { setOnClickListener { act() } }
        scan = Button(this).apply {
            text = "Scan the QR"
            visibility = View.GONE
            setOnClickListener { scanQr() }
        }
        skip = Button(this).apply {
            text = "Skip for now"
            setOnClickListener { current?.let(::skipStep) }
        }
        approveHere = Button(this).apply {
            text = "Approve on this phone instead"
            visibility = View.GONE
            setOnClickListener {
                pairingFlow.approvalUrl?.let { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it))) }
            }
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad * 2, pad, pad)
            addView(progress)
            addView(title)
            addView(why)
            addView(scan)
            addView(server)
            addView(code)
            addView(primary)
            addView(skip)
            addView(status)
            addView(approveHere)
        }
        window.decorView.setBackgroundColor(BACKGROUND)
        setContentView(ScrollView(this).apply { setBackgroundColor(BACKGROUND); addView(content) })
    }

    override fun onResume() {
        super.onResume()
        pairingFlow.resume()
        refresh()
    }

    // A setup link opened while setup is showing.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (SetupLink.apply(this, intent)) {
            server.setText(DeliveryConfig(this).serverUrl)
            refresh()
        }
    }

    private fun skipped(): Set<SetupStep> = preferences(this).getStringSet(KEY_SKIPPED, emptySet())!!
        .mapNotNull { name -> SetupStep.entries.firstOrNull { it.name == name } }.toSet()

    private fun skipStep(step: SetupStep) {
        intent.removeExtra(EXTRA_STEP)
        preferences(this).edit().putStringSet(KEY_SKIPPED, skipped().map { it.name }.toSet() + step.name).apply()
        status.text = ""
        refresh()
    }

    private fun refresh() {
        lifecycleScope.launch {
            healthDone = healthStepDone()
            val done = syncDone(this@SetupActivity) + if (healthDone) setOf(SetupStep.HEALTH) else emptySet()
            // Accessibility is on: start the root paths that keep it on.
            if (SetupStep.BLOCKING in done && (!AppSuspender.enabled(this@SetupActivity) || !GuardRoot.enabled(this@SetupActivity))) {
                withContext(Dispatchers.IO) { BlockingSetup.ensure(this@SetupActivity) }
            }
            // Asked for from the app or the panel: that step first while it is missing.
            val asked = intent.getStringExtra(EXTRA_STEP)?.let { name -> SetupStep.entries.firstOrNull { it.name == name } }?.takeIf { it !in done }
            if (asked == null) intent.removeExtra(EXTRA_STEP)
            val step = asked ?: SetupStep.next(done, skipped())
            if (step != current) status.text = ""
            current = step
            render(step, done)
        }
    }

    private fun render(step: SetupStep?, done: Set<SetupStep>) {
        if (step == null) {
            progress.text = "ALL SET"
            title.text = "This phone is ready"
            why.text = "Collection is running. You can change anything later in Settings."
            primary.text = "Open Telemetry"
            skip.visibility = View.GONE
            approveHere.visibility = View.GONE
            server.visibility = View.GONE
            code.visibility = View.GONE
            scan.visibility = View.GONE
            return
        }
        val total = SetupStep.entries.size
        progress.text = "STEP ${SetupStep.entries.indexOf(step) + 1} OF $total"
        title.text = step.title
        why.text = step.why
        primary.text = step.action
        skip.visibility = if (step.required) View.GONE else View.VISIBLE
        server.visibility = if (step == SetupStep.PAIR) View.VISIBLE else View.GONE
        code.visibility = server.visibility
        scan.visibility = server.visibility
        approveHere.visibility = if (step == SetupStep.PAIR && pairingFlow.approvalUrl != null) View.VISIBLE else View.GONE
        if (step != SetupStep.PAIR) return
        val config = DeliveryConfig(this)
        if (server.text.isBlank()) server.setText(config.serverUrl)
        if (config.removed && status.text.isBlank()) {
            status.text = "This phone was removed from Telemetry in the panel. Connect it again with a new setup code; what it recorded meanwhile is still here."
        }
        // Opened from the panel's QR: connect with its code straight away, once.
        val pending = config.setupCode
        if (pending.isNotBlank() && pending != codeTried) {
            code.setText(pending)
            codeTried = pending
            act()
        }
    }

    private fun act() {
        val step = current ?: run { finish(); return }
        when (step) {
            SetupStep.PAIR -> {
                // The whole setup link pasted into the address field works too.
                SetupLink.parse(server.text.toString())?.let { link ->
                    SetupLink.applyLink(this, server.text.toString())
                    server.setText(link.server)
                    if (link.code.isNotBlank()) code.setText(link.code)
                }
                val address = server.text.toString().trim().trimEnd('/')
                if (!address.startsWith("https://")) {
                    status.text = "Enter your Telemetry server's address, starting with https://. The panel shows it under Settings → Devices → Add a device."
                    return
                }
                DeliveryConfig(this).serverUrl = address
                pairingFlow.start(address, openBrowser = false, code = code.text.toString())
            }
            SetupStep.NOTIFICATIONS -> if (Build.VERSION.SDK_INT >= 33) {
                permissionLauncher.launch(arrayOf(Manifest.permission.POST_NOTIFICATIONS))
            }
            SetupStep.USAGE_ACCESS -> openSetting(
                Intent(Settings.ACTION_USAGE_ACCESS_SETTINGS, Uri.parse("package:$packageName")),
                Intent(Settings.ACTION_USAGE_ACCESS_SETTINGS),
            )
            SetupStep.HEALTH -> requestHealth()
            SetupStep.SAMSUNG_HEALTH -> SamsungHealthSource.request(this) { refresh() }
            SetupStep.LOCATION -> permissionLauncher.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
            SetupStep.BACKGROUND_LOCATION -> permissionLauncher.launch(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION))
            SetupStep.CALENDAR -> permissionLauncher.launch(arrayOf(Manifest.permission.READ_CALENDAR))
            SetupStep.BATTERY -> openSetting(
                Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")),
                Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS),
            )
            SetupStep.BLOCKING -> openSetting(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS), Intent(Settings.ACTION_SETTINGS))
            SetupStep.NOTIFICATION_ACCESS -> openSetting(
                if (Build.VERSION.SDK_INT >= 30) {
                    Intent(Settings.ACTION_NOTIFICATION_LISTENER_DETAIL_SETTINGS).putExtra(
                        Settings.EXTRA_NOTIFICATION_LISTENER_COMPONENT_NAME,
                        ComponentName(this, TelemetryNotificationListenerService::class.java).flattenToString(),
                    )
                } else {
                    Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS)
                },
                Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS),
            )
            SetupStep.START -> startCollection()
        }
    }

    /** Reads the panel's setup QR: the server's address and the one-time code. */
    private fun scanQr() {
        val options = GmsBarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build()
        GmsBarcodeScanning.getClient(this, options).startScan()
            .addOnSuccessListener { barcode ->
                val text = barcode.rawValue
                val link = SetupLink.parse(text)
                if (link == null) {
                    status.text = "That QR isn't a Telemetry setup code. In the panel, open Settings → Devices → Add a device and scan the QR it shows."
                    return@addOnSuccessListener
                }
                SetupLink.applyLink(this, text)
                server.setText(link.server)
                code.setText(link.code)
                codeTried = link.code
                act()
            }
            .addOnFailureListener { error ->
                // The first scan on a phone can come while Play services is
                // still fetching the scanner.
                status.text = if (error is com.google.mlkit.common.MlKitException && error.errorCode == com.google.mlkit.common.MlKitException.UNAVAILABLE) {
                    "The scanner is still being set up on this phone. Try again in a few seconds."
                } else {
                    "The scanner couldn't start here; it needs Google Play services. Scan the QR with the camera app instead, or type the address and code below."
                }
            }
    }

    private fun openSetting(preferred: Intent, fallback: Intent) {
        try {
            startActivity(preferred)
        } catch (error: Exception) {
            try {
                startActivity(fallback)
            } catch (again: Exception) {
                status.text = "This setting could not be opened on this phone."
            }
        }
    }

    private suspend fun healthStepDone(): Boolean {
        if (HealthConnectClient.getSdkStatus(this, HEALTH_CONNECT_PACKAGE) != HealthConnectClient.SDK_AVAILABLE) return false
        return try {
            val granted = HealthConnectClient.getOrCreate(this, HEALTH_CONNECT_PACKAGE).permissionController.getGrantedPermissions()
            granted.containsAll(healthPermissions)
        } catch (error: Exception) {
            false
        }
    }

    private fun requestHealth() {
        when (HealthConnectClient.getSdkStatus(this, HEALTH_CONNECT_PACKAGE)) {
            HealthConnectClient.SDK_AVAILABLE -> {
                val client = HealthConnectClient.getOrCreate(this, HEALTH_CONNECT_PACKAGE)
                val background = if (client.features.getFeatureStatus(HealthConnectFeatures.FEATURE_READ_HEALTH_DATA_IN_BACKGROUND) ==
                    HealthConnectFeatures.FEATURE_STATUS_AVAILABLE
                ) setOf(HealthPermission.PERMISSION_READ_HEALTH_DATA_IN_BACKGROUND) else emptySet()
                healthLauncher.launch(healthPermissions + background)
            }
            HealthConnectClient.SDK_UNAVAILABLE_PROVIDER_UPDATE_REQUIRED ->
                openSetting(Intent(Intent.ACTION_VIEW, Uri.parse("market://details?id=$HEALTH_CONNECT_PACKAGE")), Intent(Settings.ACTION_SETTINGS))
            else -> status.text = "Health Connect is not available on this phone. You can skip this step."
        }
    }

    private fun startCollection() {
        try {
            ContextCompat.startForegroundService(this, Intent(this, TelemetryCollectionService::class.java))
            if (granted(this, Manifest.permission.ACCESS_FINE_LOCATION) || granted(this, Manifest.permission.ACCESS_COARSE_LOCATION)) {
                ContextCompat.startForegroundService(this, Intent(this, TelemetryLocationService::class.java))
            }
            BackgroundJobs.scheduleDelivery(this)
            preferences(this).edit().putBoolean(KEY_STARTED, true).apply()
            refresh()
        } catch (error: Exception) {
            status.text = "Could not start collection: ${error.javaClass.simpleName}"
        }
    }
}
