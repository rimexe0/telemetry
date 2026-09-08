package cyou.rime.telemetry.collector

import android.accessibilityservice.AccessibilityService
import android.graphics.PixelFormat
import android.view.Gravity
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView

/**
 * Optional, owner-enabled soft block. It is inert until both the policy switch
 * and a non-empty package list are saved in the setup screen.
 */
class TelemetryAccessibilityService : AccessibilityService() {
    private var overlay: LinearLayout? = null

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        if (event.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED &&
            event.eventType != AccessibilityEvent.TYPE_WINDOWS_CHANGED
        ) return
        val packageName = event.packageName?.toString().orEmpty()
        val policy = PolicyStore(this)
        if (!policy.softBlockEnabled || packageName.isBlank() || packageName == applicationContext.packageName) return
        if (packageName !in policy.blockedPackages) return
        showOverlay(packageName)
    }

    override fun onInterrupt() = removeOverlay()

    override fun onDestroy() {
        removeOverlay()
        super.onDestroy()
    }

    private fun showOverlay(packageName: String) {
        if (overlay != null) return
        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(48, 48, 48, 48)
            setBackgroundColor(0xFFF8F8F8.toInt())
        }
        container.addView(TextView(this).apply {
            text = "Telemetry pause"
            textSize = 24f
        })
        container.addView(TextView(this).apply {
            text = "This app is in your configured soft-block list. Return to the plan or disable the policy from Telemetry."
            textSize = 16f
        })
        container.addView(Button(this).apply {
            text = "Return to home"
            setOnClickListener {
                performGlobalAction(GLOBAL_ACTION_HOME)
                removeOverlay()
            }
        })
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.CENTER
        }
        getSystemService(WindowManager::class.java).addView(container, params)
        overlay = container
    }

    private fun removeOverlay() {
        overlay?.let { view ->
            runCatching { getSystemService(WindowManager::class.java).removeView(view) }
        }
        overlay = null
    }
}
