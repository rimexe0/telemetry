package cyou.rime.telemetry.collector

import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant

/**
 * The owner's everyday screen: what to do now, why something is closed, and
 * where to return. Collection and pairing controls live in [SettingsActivity].
 */
class MainActivity : ComponentActivity() {
    private companion object {
        const val CACHE = "telemetry-now"
        const val KEY_JSON = "json"
        const val KEY_FETCHED_AT = "fetched_at"
        const val REFRESH_MS = 60_000L
        val BACKGROUND = Color.rgb(17, 18, 19)
        val FOREGROUND = Color.rgb(238, 238, 235)
        val MUTED = Color.rgb(170, 169, 164)
        val SOFT = Color.rgb(36, 38, 40)
    }

    private lateinit var mode: TextView
    private lateinit var title: TextView
    private lateinit var state: TextView
    private lateinit var next: TextView
    private lateinit var resume: TextView
    private lateinit var reason: TextView
    private lateinit var footer: TextView
    private lateinit var setup: Button

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val density = resources.displayMetrics.density
        val pad = (24 * density).toInt()
        fun text(size: Float, color: Int = FOREGROUND, bold: Boolean = false) = TextView(this).apply {
            textSize = size
            setTextColor(color)
            if (bold) typeface = Typeface.DEFAULT_BOLD
            setPadding(0, (6 * density).toInt(), 0, (6 * density).toInt())
        }
        mode = text(14f, MUTED, bold = true).apply { letterSpacing = 0.12f }
        title = text(30f, bold = true)
        state = text(15f, MUTED)
        next = text(19f)
        resume = text(17f).apply {
            background = GradientDrawable().apply { setColor(SOFT); cornerRadius = 8 * density }
            setPadding(pad / 2, pad / 2, pad / 2, pad / 2)
            visibility = View.GONE
        }
        reason = text(16f, MUTED)
        footer = text(13f, MUTED)
        setup = Button(this).apply {
            text = "Set up this phone"
            visibility = View.GONE
            setOnClickListener { openSettings() }
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad * 2, pad, pad)
            addView(mode)
            addView(title)
            addView(state)
            addView(next)
            addView(resume)
            addView(reason)
            addView(setup)
            addView(Button(this@MainActivity).apply {
                text = "Start, stuck, done… (opens Now)"
                setOnClickListener { openPanel() }
            })
            addView(footer)
            addView(Button(this@MainActivity).apply {
                text = "Settings and collection"
                setOnClickListener { openSettings() }
            }, LinearLayout.LayoutParams(LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT).apply {
                gravity = Gravity.START
                topMargin = pad
            })
        }
        window.decorView.setBackgroundColor(BACKGROUND)
        setContentView(ScrollView(this).apply { setBackgroundColor(BACKGROUND); addView(content) })
        showCached()

        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                while (true) {
                    refresh()
                    delay(REFRESH_MS)
                }
            }
        }
    }

    private fun openSettings() = startActivity(Intent(this, SettingsActivity::class.java))

    private fun openPanel() {
        val server = DeliveryConfig(this).serverUrl.trimEnd('/')
        startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("$server/panel/")))
    }

    private suspend fun refresh() {
        val config = DeliveryConfig(this)
        val token = config.token()
        if (token == null) {
            setup.visibility = View.VISIBLE
            if (!showCached()) {
                mode.text = "NOT SET UP"
                title.text = "This phone is not paired yet"
                reason.text = "Pair it in Settings to see your current task and follow your plan."
            }
            return
        }
        setup.visibility = View.GONE
        val body = withContext(Dispatchers.IO) {
            runCatching { fetch(config.serverUrl, DeviceIdentity.get(this@MainActivity), token) }.getOrNull()
        }
        if (body != null) {
            getSharedPreferences(CACHE, MODE_PRIVATE).edit()
                .putString(KEY_JSON, body)
                .putString(KEY_FETCHED_AT, Instant.now().toString())
                .apply()
            render(NowModel.parse(body, Instant.now(), Instant.now(), offline = false))
        } else if (!showCached()) {
            mode.text = "OFFLINE"
            title.text = "No plan information yet"
            reason.text = "The server could not be reached and nothing is cached. Nothing is assumed."
        }
    }

    /** Shows the cached state, following its timeline while offline. */
    private fun showCached(): Boolean {
        val preferences = getSharedPreferences(CACHE, MODE_PRIVATE)
        val json = preferences.getString(KEY_JSON, null) ?: return false
        val fetchedAt = preferences.getString(KEY_FETCHED_AT, null)?.let(Instant::parse) ?: return false
        val now = Instant.now()
        if (!NowModel.timelineCovers(json, now)) {
            mode.text = "OFFLINE"
            title.text = "Plan information is out of date"
            reason.text = "Last update ${NowModel.ageText(fetchedAt, now)}. Nothing is assumed until the server is reachable."
            return true
        }
        render(NowModel.parse(json, fetchedAt, now, offline = true))
        return true
    }

    private fun render(view: NowView) {
        mode.text = view.modeLabel.uppercase()
        title.text = view.taskTitle ?: if (view.planExists) "Nothing scheduled right now" else "No plan for today"
        state.text = view.taskState.orEmpty()
        state.visibility = if (view.taskState == null) View.GONE else View.VISIBLE
        next.text = view.nextAction?.let { "Next: $it" }.orEmpty()
        next.visibility = if (view.nextAction == null) View.GONE else View.VISIBLE
        resume.text = view.resumeMarker?.let { "RETURN HERE: $it" }.orEmpty()
        resume.visibility = if (view.resumeMarker == null) View.GONE else View.VISIBLE
        reason.text = view.reason
        val age = NowModel.ageText(view.asOf, Instant.now())
        footer.text = if (view.offline) "Offline: following the plan cached $age." else "Updated $age."
    }

    private fun fetch(server: String, deviceId: String, token: String): String {
        val connection = URL(server.trimEnd('/') + "/v1/device/now").openConnection() as HttpURLConnection
        try {
            connection.connectTimeout = 10_000
            connection.readTimeout = 10_000
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("X-Telemetry-Device-ID", deviceId)
            connection.setRequestProperty("Accept", "application/json")
            check(connection.responseCode == 200) { "now request failed with ${connection.responseCode}" }
            return connection.inputStream.bufferedReader().use { it.readText() }
        } finally {
            connection.disconnect()
        }
    }
}
