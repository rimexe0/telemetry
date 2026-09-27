package cyou.rime.telemetry.collector

import android.annotation.SuppressLint
import android.graphics.Color
import android.os.Bundle
import android.webkit.CookieManager
import android.widget.FrameLayout
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.addCallback
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * The Telemetry panel inside the app. The paired device credential is traded
 * for a short-lived panel session, so there is no separate login here.
 */
class PanelActivity : ComponentActivity() {
    private lateinit var web: WebView

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val config = DeliveryConfig(this)
        val token = config.token()
        if (token == null) {
            setContentView(TextView(this).apply {
                text = "Finish setting up this phone first."
                setPadding(48, 96, 48, 48)
            })
            return
        }
        web = WebView(this).apply {
            setBackgroundColor(Color.rgb(17, 18, 19))
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            webViewClient = WebViewClient()
        }
        // Keep the page clear of the status and navigation bars (edge-to-edge on Android 15+).
        val frame = FrameLayout(this).apply {
            setBackgroundColor(Color.rgb(17, 18, 19))
            addView(web)
        }
        ViewCompat.setOnApplyWindowInsetsListener(frame) { view, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
            view.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            WindowInsetsCompat.CONSUMED
        }
        setContentView(frame)
        onBackPressedDispatcher.addCallback(this) {
            if (web.canGoBack()) web.goBack() else finish()
        }
        val server = config.serverUrl.trimEnd('/')
        lifecycleScope.launch {
            val cookie = withContext(Dispatchers.IO) {
                runCatching { requestSession(server, DeviceIdentity.get(this@PanelActivity), token) }.getOrNull()
            }
            if (cookie != null) {
                CookieManager.getInstance().apply {
                    setAcceptCookie(true)
                    setCookie(server, "$cookie; Path=/; Secure; HttpOnly")
                    flush()
                }
            }
            web.loadUrl("$server/panel/")
        }
    }

    private fun requestSession(server: String, deviceId: String, token: String): String {
        val connection = URL("$server/v1/device/panel-session").openConnection() as HttpURLConnection
        try {
            connection.requestMethod = "POST"
            connection.connectTimeout = 10_000
            connection.readTimeout = 10_000
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("X-Telemetry-Device-ID", deviceId)
            check(connection.responseCode == 200) { "panel session failed with ${connection.responseCode}" }
            return JSONObject(connection.inputStream.bufferedReader().use { it.readText() }).getString("cookie")
        } finally {
            connection.disconnect()
        }
    }
}
