package cyou.rime.telemetry.collector

import android.accessibilityservice.AccessibilityService
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.Typeface
import android.util.Log
import android.view.Gravity
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import java.time.Instant

/**
 * Stage-1 blocking (#20). When the approved policy blocks the app coming to the
 * foreground, a full-screen reason covers it and the owner is sent home. Every
 * block is recorded. It follows the cached server timeline, so it keeps working
 * offline, and it never blocks when that cache is missing or out of date.
 */
class TelemetryAccessibilityService : AccessibilityService() {
    private companion object {
        const val TAG = "TelemetryBlocking"
    }

    private var overlay: LinearLayout? = null
    private var systemSafe: Set<String> = emptySet()
    private var cached: Pair<Instant, String>? = null
    private var lastRecorded = ""

    override fun onServiceConnected() {
        super.onServiceConnected()
        systemSafe = PolicyEvents.systemSafe(this)
        // Android reconnects this service after updates and reboots.
        CollectionStarter.ensureRunning(this)
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        if (event.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) return
        val pkg = event.packageName?.toString().orEmpty()
        if (pkg.isBlank() || pkg == "com.android.systemui") return
        val decision = currentDecision()
        if (!BlockPolicy.shouldBlock(decision, pkg, systemSafe)) {
            // Keyboards, popups and toasts are not full screen; the blocked app
            // is still underneath them.
            if (pkg != packageName && event.isFullScreen) removeOverlay()
            return
        }
        Log.i(TAG, "blocking $pkg (${decision!!.mode})")
        showOverlay(decision)
        val key = "block:$pkg:${Instant.now().epochSecond / 60}"
        if (key != lastRecorded) {
            lastRecorded = key
            Thread { runCatching { PolicyEvents.record(this, "android.policy_block", key, decision, mapOf("package" to pkg)) } }.start()
        }
    }

    private fun currentDecision(): BlockDecision? {
        val entry = NowCache.read(this) ?: return null
        val json = if (cached?.first == entry.fetchedAt) cached!!.second else entry.json.also { cached = entry.fetchedAt to it }
        return runCatching { BlockPolicy.at(json, Instant.now()) }.getOrNull()
    }

    override fun onInterrupt() = removeOverlay()

    override fun onDestroy() {
        removeOverlay()
        super.onDestroy()
    }

    private fun showOverlay(decision: BlockDecision) {
        val density = resources.displayMetrics.density
        val pad = (28 * density).toInt()
        val title = NowModel.modeLabel(decision.mode).uppercase()
        val existing = overlay
        if (existing != null) {
            (existing.getChildAt(1) as? TextView)?.text = decision.reason
            return
        }
        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad, pad, pad, pad)
            setBackgroundColor(Color.rgb(17, 18, 19))
        }
        container.addView(TextView(this).apply {
            text = title
            textSize = 15f
            letterSpacing = 0.12f
            setTextColor(Color.rgb(170, 169, 164))
            typeface = Typeface.DEFAULT_BOLD
        })
        container.addView(TextView(this).apply {
            text = decision.reason
            textSize = 26f
            setTextColor(Color.rgb(238, 238, 235))
            setPadding(0, pad / 2, 0, pad)
        })
        container.addView(TextView(this).apply {
            text = "This app is closed right now."
            textSize = 16f
            setTextColor(Color.rgb(170, 169, 164))
            setPadding(0, 0, 0, pad)
        })
        container.addView(Button(this).apply {
            text = "Go back"
            setOnClickListener {
                performGlobalAction(GLOBAL_ACTION_HOME)
                removeOverlay()
            }
        })
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS,
            PixelFormat.OPAQUE,
        ).apply {
            // Cover the status bar and cutout too, so nothing of the blocked app shows through.
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
        }
        runCatching { getSystemService(WindowManager::class.java).addView(container, params) }
            .onSuccess { overlay = container }
    }

    private fun removeOverlay() {
        overlay?.let { view -> runCatching { getSystemService(WindowManager::class.java).removeView(view) } }
        overlay = null
    }
}
