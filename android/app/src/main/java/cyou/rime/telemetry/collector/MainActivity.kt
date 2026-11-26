package cyou.rime.telemetry.collector

import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.text.SpannableString
import android.text.Spanned
import android.text.style.StyleSpan
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant

/**
 * The app: the Telemetry panel, full screen, signed in with this phone's
 * paired credential. What has to stay on the phone stays native: setup on
 * first run, a one-line strip when setup needs attention, Settings and setup
 * opened from the panel's telemetry-app:// links ([PanelLinks]), and a small
 * screen when the server can't be reached.
 */
class MainActivity : ComponentActivity() {
    private companion object {
        const val REFRESH_MS = 60_000L
    }

    private enum class Showing { NOTHING, NOT_PAIRED, OPENING, PANEL, UNREACHABLE }

    private lateinit var web: WebView
    private lateinit var strip: TextView
    private lateinit var status: LinearLayout
    private lateinit var statusTitle: TextView
    private lateinit var statusBody: TextView
    private lateinit var statusPrimary: Button
    private lateinit var statusSecondary: Button
    private lateinit var back: OnBackPressedCallback

    private var showing = Showing.NOTHING
    private var server = ""
    private var loadFailed = false
    private var sessionOpenedAt = Instant.EPOCH
    private var sessionExpiresAt: Instant? = null
    private var opening: Job? = null
    private var attention: SetupStep? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT), SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT))
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) SetupLink.apply(this, intent)
        buildViews()
        CollectionStarter.ensureRunning(this)
        if (savedInstanceState == null && SetupActivity.needsSetup(this)) openSetup(null)

        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                while (true) {
                    check()
                    delay(REFRESH_MS)
                }
            }
        }
    }

    override fun onResume() {
        super.onResume()
        // Back after switching the network or the VPN on: try again without a tap.
        if (showing == Showing.UNREACHABLE) open()
    }

    // A setup link or a notification while the app is open.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        SetupLink.apply(this, intent)
        if (SetupActivity.needsSetup(this)) openSetup(null)
    }

    override fun onDestroy() {
        if (::web.isInitialized) runCatching { web.destroy() }
        super.onDestroy()
    }

    /** Pairing, the strip, the panel session, and the cached policy blocking reads. */
    private suspend fun check() {
        val config = DeliveryConfig(this)
        if (config.token() == null) {
            showNotPaired(config.removed)
            return
        }
        attention = SetupStep.missing(SetupActivity.syncDone(this)).firstOrNull()
        strip.visibility = if (attention != null) View.VISIBLE else View.GONE
        when (showing) {
            Showing.NOTHING, Showing.NOT_PAIRED -> open()
            Showing.PANEL -> if (PanelSession.renewDue(sessionExpiresAt, sessionOpenedAt, Instant.now())) renew()
            Showing.OPENING, Showing.UNREACHABLE -> Unit
        }
        // Keeps the policy the block screen reads fresh while the app is open.
        withContext(Dispatchers.IO) { runCatching { NowCache.refresh(this@MainActivity) } }
    }

    private fun openSetup(step: SetupStep?) = startActivity(SetupActivity.intent(this, step))

    /** Trades the paired credential for a panel session, then loads the panel. */
    private fun open() {
        if (opening?.isActive == true) return
        val config = DeliveryConfig(this)
        val token = config.token() ?: return showNotPaired(config.removed)
        server = config.serverUrl.trimEnd('/')
        show(Showing.OPENING)
        opening = lifecycleScope.launch {
            val result = withContext(Dispatchers.IO) { PanelSession.request(server, DeviceIdentity.get(this@MainActivity), token) }
            when (result) {
                is PanelSession.Result.Opened -> {
                    keep(result)
                    loadFailed = false
                    web.loadUrl("$server/panel/")
                }
                is PanelSession.Result.Refused -> showUnreachable(result.status)
                PanelSession.Result.Unreachable -> showUnreachable(null)
            }
        }
    }

    /** A new session before the old one ends; the open page keeps going with it. */
    private fun renew() {
        if (opening?.isActive == true) return
        val token = DeliveryConfig(this).token() ?: return
        opening = lifecycleScope.launch {
            val result = withContext(Dispatchers.IO) { PanelSession.request(server, DeviceIdentity.get(this@MainActivity), token) }
            val expired = sessionExpiresAt?.isAfter(Instant.now()) != true
            when {
                result is PanelSession.Result.Opened -> {
                    keep(result)
                    if (expired) web.reload()
                }
                expired -> showUnreachable((result as? PanelSession.Result.Refused)?.status)
            }
        }
    }

    private fun keep(session: PanelSession.Result.Opened) {
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setCookie(server, "${session.cookie}; Path=/; Secure; HttpOnly")
            flush()
        }
        sessionOpenedAt = Instant.now()
        sessionExpiresAt = session.expiresAt
    }

    /** Follows a link the panel opens: native screens for telemetry-app://, the browser for other sites. */
    private fun follow(url: String, mainFrame: Boolean): Boolean {
        when (val route = PanelLinks.route(url, server, mainFrame)) {
            PanelRoute.InPanel -> return false
            is PanelRoute.Browser -> runCatching { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(route.url))) }
            is PanelRoute.Settings -> startActivity(SettingsActivity.intent(this, route.section))
            is PanelRoute.Setup -> openSetup(route.step)
            PanelRoute.Refused -> Unit
        }
        return true
    }

    private fun showNotPaired(removed: Boolean) {
        strip.visibility = View.GONE
        if (removed) {
            status("This phone was removed from Telemetry",
                "It was removed in the panel. Connect it again with a new setup code; what it recorded meanwhile is kept.")
        } else {
            status("This phone isn't connected yet", "Finish setting it up to open Telemetry here.")
        }
        statusPrimary.text = "Set up"
        statusPrimary.setOnClickListener { openSetup(SetupStep.PAIR) }
        statusPrimary.visibility = View.VISIBLE
        statusSecondary.visibility = View.VISIBLE
        show(Showing.NOT_PAIRED)
    }

    /** Never a blank page: what happened, what still works, and Retry. */
    private fun showUnreachable(refusedWith: Int?) {
        val essentials = "Blocking keeps working on this phone, and calls, maps and the other essentials stay open."
        if (refusedWith == null) {
            status("Can't reach your Telemetry server", essentials)
        } else {
            status("Your Telemetry server didn't open the panel", "It answered $refusedWith. $essentials")
        }
        statusPrimary.text = "Retry"
        statusPrimary.setOnClickListener { open() }
        statusPrimary.visibility = View.VISIBLE
        statusSecondary.visibility = View.VISIBLE
        show(Showing.UNREACHABLE)
    }

    private fun status(title: String, body: String) {
        statusTitle.text = title
        statusTitle.visibility = View.VISIBLE
        statusBody.text = body
    }

    private fun show(next: Showing) {
        showing = next
        if (next == Showing.OPENING) {
            statusTitle.visibility = View.GONE
            statusBody.text = "Opening Telemetry…"
            statusPrimary.visibility = View.GONE
            statusSecondary.visibility = View.GONE
        }
        web.visibility = if (next == Showing.PANEL) View.VISIBLE else View.INVISIBLE
        status.visibility = if (next == Showing.PANEL) View.GONE else View.VISIBLE
        back.isEnabled = next == Showing.PANEL && web.canGoBack()
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun buildViews() {
        val background = getColor(R.color.background)
        val foreground = getColor(R.color.foreground)
        val muted = getColor(R.color.muted)
        val density = resources.displayMetrics.density
        fun dp(value: Int) = (value * density).toInt()

        web = WebView(this).apply {
            setBackgroundColor(background)
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            settings.userAgentString = PanelLinks.userAgent(settings.userAgentString, BuildConfig.VERSION_NAME)
            webViewClient = PanelClient()
            visibility = View.INVISIBLE
        }

        // One line, only while setup needs something; it opens that step.
        strip = TextView(this).apply {
            val text = "Setup needs attention · Fix"
            this.text = SpannableString(text).apply { setSpan(StyleSpan(Typeface.BOLD), text.length - 3, text.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE) }
            contentDescription = "Setup needs attention. Fix"
            textSize = 14f
            setTextColor(foreground)
            setBackgroundColor(getColor(R.color.soft))
            gravity = Gravity.CENTER_VERTICAL
            minHeight = dp(44)
            setPadding(dp(16), dp(8), dp(16), dp(8))
            visibility = View.GONE
            setOnClickListener { openSetup(attention) }
        }

        statusTitle = TextView(this).apply {
            textSize = 22f
            setTextColor(foreground)
            typeface = Typeface.DEFAULT_BOLD
            setPadding(0, 0, 0, dp(8))
        }
        statusBody = TextView(this).apply {
            textSize = 16f
            setTextColor(muted)
            setPadding(0, 0, 0, dp(16))
        }
        statusPrimary = Button(this)
        statusSecondary = Button(this).apply {
            text = "Phone settings"
            setOnClickListener { startActivity(SettingsActivity.intent(this@MainActivity, null)) }
        }
        status = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(24), dp(24), dp(24), dp(24))
            addView(statusTitle)
            addView(statusBody)
            addView(statusPrimary, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
            addView(statusSecondary, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        }

        val body = FrameLayout(this).apply {
            addView(web, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(status, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        }
        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(strip, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
            addView(body, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        }
        val root = FrameLayout(this).apply {
            setBackgroundColor(background)
            addView(column)
        }
        // Edge to edge: the page sits clear of the bars, the cutout and the keyboard.
        ViewCompat.setOnApplyWindowInsetsListener(root) { view, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val keyboard = insets.getInsets(WindowInsetsCompat.Type.ime())
            view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, keyboard.bottom))
            WindowInsetsCompat.CONSUMED
        }
        setContentView(root)

        // Back walks the panel's history first, then leaves as Android does.
        back = object : OnBackPressedCallback(false) {
            override fun handleOnBackPressed() {
                if (web.canGoBack()) web.goBack()
            }
        }
        onBackPressedDispatcher.addCallback(this, back)
    }

    private inner class PanelClient : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
            follow(request.url.toString(), request.isForMainFrame)

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (!request.isForMainFrame) return
            loadFailed = true
            showUnreachable(null)
        }

        // A proxy or relay that answers for a server it can't reach.
        override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, response: WebResourceResponse) {
            if (!request.isForMainFrame || response.statusCode < 500) return
            loadFailed = true
            showUnreachable(null)
        }

        override fun onPageFinished(view: WebView, url: String) {
            if (!loadFailed && showing == Showing.OPENING) show(Showing.PANEL)
        }

        override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
            back.isEnabled = showing == Showing.PANEL && view.canGoBack()
        }

        // The page's renderer died: start over instead of crashing the app.
        override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
            (view.parent as? ViewGroup)?.removeView(view)
            view.destroy()
            recreate()
            return true
        }
    }
}
