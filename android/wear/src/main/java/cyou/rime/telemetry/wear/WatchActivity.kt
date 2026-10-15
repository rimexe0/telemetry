package cyou.rime.telemetry.wear

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.Gravity
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView

/** The watch screen: what it is doing, the permissions it needs, and a walk by hand. */
class WatchActivity : Activity() {
    private lateinit var status: TextView
    private lateinit var allow: Button
    private lateinit var walk: Button
    private val handler = Handler(Looper.getMainLooper())
    private val refresh = object : Runnable {
        override fun run() {
            show()
            handler.postDelayed(this, 2_000L)
        }
    }

    private val foreground = buildList {
        add(Manifest.permission.ACCESS_FINE_LOCATION)
        add(Manifest.permission.ACTIVITY_RECOGNITION)
        add(if (Build.VERSION.SDK_INT >= 36) "android.permission.health.READ_HEART_RATE" else Manifest.permission.BODY_SENSORS)
        if (Build.VERSION.SDK_INT >= 33) add(Manifest.permission.POST_NOTIFICATIONS)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val padding = (12 * resources.displayMetrics.density).toInt()
        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(padding, padding * 3, padding, padding * 3)
        }
        status = TextView(this).apply { gravity = Gravity.CENTER; textSize = 15f }
        allow = Button(this).apply { text = "Allow"; setOnClickListener { askNext() } }
        walk = Button(this).apply {
            setOnClickListener {
                OutsideService.start(this@WatchActivity, if (OutsideService.current == null) OutsideService.ACTION_START else OutsideService.ACTION_STOP)
                handler.postDelayed({ show() }, 500)
            }
        }
        column.addView(status)
        column.addView(allow)
        column.addView(walk)
        setContentView(ScrollView(this).apply { addView(column) })
    }

    override fun onResume() {
        super.onResume()
        if (missing().isEmpty() && background()) OutsideService.start(this)
        handler.post(refresh)
    }

    override fun onPause() {
        handler.removeCallbacks(refresh)
        super.onPause()
    }

    private fun missing() = foreground.filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }

    private fun background() = checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun askNext() {
        val missing = missing()
        when {
            missing.isNotEmpty() -> requestPermissions(missing.toTypedArray(), 1)
            // Location all the time is set in the app's settings on Wear OS.
            !background() -> runCatching { requestPermissions(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION), 2) }
                .onFailure { startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName"))) }
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (missing().isEmpty() && background()) OutsideService.start(this)
        show()
    }

    private fun show() {
        val needs = missing().size + if (background()) 0 else 1
        allow.visibility = if (needs > 0) Button.VISIBLE else Button.GONE
        allow.text = if (missing().isNotEmpty()) "Allow" else "Location all the time"
        status.text = if (needs > 0) "Telemetry needs location (all the time), activity and heart rate to record walks." else OutsideService.state
        walk.visibility = if (needs > 0) Button.GONE else Button.VISIBLE
        walk.text = if (OutsideService.current == null) "Start walk" else "Stop walk"
    }
}
