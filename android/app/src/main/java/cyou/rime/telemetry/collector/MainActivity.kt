package cyou.rime.telemetry.collector

import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import cyou.rime.telemetry.collector.ui.HabitEditing
import cyou.rime.telemetry.collector.ui.HabitsScreen
import cyou.rime.telemetry.collector.ui.PText
import cyou.rime.telemetry.collector.ui.PanelUnavailable
import cyou.rime.telemetry.collector.ui.Place
import cyou.rime.telemetry.collector.ui.PlaceBar
import cyou.rime.telemetry.collector.ui.Small
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import cyou.rime.telemetry.collector.ui.TodayScreen
import cyou.rime.telemetry.collector.ui.panelColors
import cyou.rime.telemetry.collector.ui.tapRow
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * The app: four places along the bottom, as in the panel (owner 2026-11-26).
 * Today and Habits are drawn here from answers saved on the phone, so they
 * keep working while the server is down; a tick is saved on the phone first
 * and sent when the server can be reached ([HabitTicks]). Activity and
 * Settings are the panel itself, signed in with this phone's credential and
 * loaded through [PanelProxy]; without the server they say so in one line.
 * Setup on first run, the setup strip, and Settings and setup opened from
 * the panel's telemetry-app:// links ([PanelLinks]) stay as they were.
 */
class MainActivity : ComponentActivity() {
    private companion object {
        const val REFRESH_MS = 60_000L
        /** The year's heatmap is fetched at most this often. */
        val YEAR_EVERY: Duration = Duration.ofMinutes(10)
        const val ACTIVITY_HASH = "#activity"
        const val SETTINGS_HASH = "#settings"
    }

    /** Where the panel stands in its web view. */
    private sealed interface Web {
        data object Closed : Web
        data object Opening : Web
        data object Open : Web
        data class Unavailable(val refusedWith: Int?) : Web
    }

    private lateinit var web: WebView

    private var place by mutableStateOf(Place.TODAY)
    // Kept in memory for the life of the app, so each place shows at once on every visit.
    private var saved by mutableStateOf(HomeMemory.memory.current ?: Saved(null, null, null, null, emptyList()))
    /** Null until the first try; false while the server can't be reached. */
    private var reachable by mutableStateOf<Boolean?>(null)
    private var savedAt by mutableStateOf<Instant?>(null)
    private var webState by mutableStateOf<Web>(Web.Closed)
    private var paired by mutableStateOf(true)
    private var removed by mutableStateOf(false)
    private var attention by mutableStateOf<SetupStep?>(null)
    private var notice by mutableStateOf<String?>(null)
    /** Why an edit on the Habits screen was refused, once. */
    private var editNotice by mutableStateOf<String?>(null)
    private var doorNotice by mutableStateOf<String?>(null)
    private var doorBusy by mutableStateOf(false)
    private var canGoBack by mutableStateOf(false)

    private var server = ""
    private var loadFailed = false
    private var proxy: PanelProxy? = null
    private var opening: Job? = null
    private var yearFetchedAt: Instant = Instant.EPOCH
    /** One talk with the server at a time, so a fetch never lands over an edit just sent. */
    private val talking = Mutex()
    private var sending: Job? = null
    private var sendAgain = false
    /** The last address of each web place, so each comes back where it was left. */
    private var activityHash = ACTIVITY_HASH
    private var settingsHash = SETTINGS_HASH

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT), SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT))
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) SetupLink.apply(this, intent)
        web = buildWebView()
        DeliveryConfig(this).let { config ->
            paired = config.token() != null
            removed = config.removed
        }
        // The saved copy at once; the server is asked after.
        reload()
        setContent { TelemetryTheme { App() } }
        CollectionStarter.ensureRunning(this)
        if (savedInstanceState == null && SetupActivity.needsSetup(this)) openSetup(null)
        // The panel loads behind Today, so the first tap on Activity or Settings shows it at once.
        if (paired && !removed) openPanel(activityHash)

        // A tick from the home-screen widget shows here at once.
        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.STARTED) {
                HabitWidgets.version.collect { reload() }
            }
        }
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
        if (webState is Web.Unavailable && place.web) openPanel(hashFor(place))
    }

    // A setup link or a notification while the app is open.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        SetupLink.apply(this, intent)
        // The widget's head opens Today.
        if (intent.action == HabitWidgets.OPEN_TODAY) place = Place.TODAY
        if (SetupActivity.needsSetup(this)) openSetup(null)
    }

    override fun onDestroy() {
        if (::web.isInitialized) runCatching { web.destroy() }
        proxy?.close()
        super.onDestroy()
    }

    private val Place.web: Boolean get() = this == Place.ACTIVITY || this == Place.SETTINGS

    private fun hashFor(place: Place): String = if (place == Place.SETTINGS) settingsHash else activityHash

    /** Reads the saved answers and the queues again; what has not changed comes from memory. */
    private fun reload() {
        saved = Saved.read(this)
        savedAt = NowCache.read(this)?.fetchedAt
        HabitTicks.takeNotice(this)?.let { notice = it }
        HabitEdits.takeNotice(this)?.let { editNotice = it }
    }

    /** Pairing, the strip, the waiting ticks, and fresh answers to save. */
    private suspend fun check() {
        val config = DeliveryConfig(this)
        if (config.token() == null) {
            paired = false
            removed = config.removed
            return
        }
        paired = true
        attention = SetupStep.missing(SetupActivity.syncDone(this)).firstOrNull()
        val wantYear = place == Place.HABITS || saved.year == null || Duration.between(yearFetchedAt, Instant.now()) >= YEAR_EVERY
        reachable = talking.withLock { withContext(Dispatchers.IO) {
            runCatching { HabitEdits.flush(this@MainActivity) }
            runCatching { HabitTicks.flush(this@MainActivity) }
            // Keeps the policy the block screen reads fresh while the app is open.
            val fresh = runCatching { NowCache.refresh(this@MainActivity) }.getOrNull() != null
            if (fresh) {
                runCatching { HabitCache.refresh(this@MainActivity, HabitCache.LIST) }
                runCatching { HabitCache.refresh(this@MainActivity, HabitCache.WEEK) }
                if (wantYear && runCatching { HabitCache.refresh(this@MainActivity, HabitCache.YEAR) }.getOrDefault(false)) yearFetchedAt = Instant.now()
            }
            if (webState == Web.Open) runCatching { proxy?.renewIfDue() }
            // Read here, off the main thread, so reload() finds it in memory.
            runCatching { HomeMemory.read(this@MainActivity) }
            fresh
        } }
        reload()
    }

    /**
     * An edit on the Habits screen: saved on the phone and on the screen at
     * once, then sent. One that can't be sent now waits in [HabitEdits] and
     * goes with the next check.
     */
    private fun edit(record: (EditQueue) -> Unit) {
        record(HabitEdits.queue(this))
        editNotice = null
        reload()
        if (sending?.isActive == true) {
            sendAgain = true
            return
        }
        sending = lifecycleScope.launch {
            do {
                sendAgain = false
                talking.withLock {
                    withContext(Dispatchers.IO) {
                        val flushed = runCatching { HabitEdits.flush(this@MainActivity) }.getOrNull()
                        if (flushed != null && flushed.sent > 0) {
                            // Today's names and the year's bands follow the edit.
                            runCatching { NowCache.refresh(this@MainActivity) }
                            runCatching { HabitCache.refresh(this@MainActivity, HabitCache.LIST) }
                            runCatching { HabitCache.refresh(this@MainActivity, HabitCache.WEEK) }
                            if (runCatching { HabitCache.refresh(this@MainActivity, HabitCache.YEAR) }.getOrDefault(false)) yearFetchedAt = Instant.now()
                        }
                        runCatching { HomeMemory.read(this@MainActivity) }
                    }
                }
                reload()
            } while (sendAgain)
        }
    }

    /** A tick or untick: on the screen and saved at once, sent when the server can be reached. */
    private fun tick(habitId: Long, day: String, done: Boolean) {
        lifecycleScope.launch {
            withContext(Dispatchers.IO) { HabitTicks.record(this@MainActivity, habitId, day, done) }
            reload()
            withContext(Dispatchers.IO) {
                runCatching { HabitTicks.flush(this@MainActivity) }
                runCatching { HabitCache.refresh(this@MainActivity, HabitCache.WEEK) }
            }
            reload()
        }
    }

    private fun cancelDoor(id: Long) {
        doorBusy = true
        doorNotice = null
        lifecycleScope.launch {
            val outcome = withContext(Dispatchers.IO) { Door.cancel(this@MainActivity, id) }
            doorNotice = when (outcome) {
                is Door.Outcome.Done -> null
                is Door.Outcome.Refused -> outcome.message
                Door.Outcome.Unreachable -> "The server can't be reached, so the door can't be cancelled now."
            }
            doorBusy = false
            reload()
        }
    }

    private fun select(next: Place, hash: String? = null) {
        // Tapping the place in use again goes back to its start.
        if (next == place && hash == null) {
            if (next == Place.SETTINGS) settingsHash = SETTINGS_HASH else if (next == Place.ACTIVITY) activityHash = ACTIVITY_HASH
        }
        place = next
        if (!next.web) return
        hash?.let { if (next == Place.SETTINGS) settingsHash = it else activityHash = it }
        when (webState) {
            Web.Open -> go(hashFor(next))
            Web.Opening -> Unit
            Web.Closed, is Web.Unavailable -> openPanel(hashFor(next))
        }
    }

    /** Moves the open panel to an address without adding to its history. */
    private fun go(hash: String) {
        val safe = PanelLinks.safeHash(hash) ?: return
        web.evaluateJavascript("location.replace('$safe')", null)
    }

    private fun openSetup(step: SetupStep?) = startActivity(SetupActivity.intent(this, step))

    /**
     * Trades the paired credential for a panel session, through the relay
     * first, then loads the panel from [PanelProxy], so it opens with or
     * without Tailscale.
     */
    private fun openPanel(hash: String) {
        if (opening?.isActive == true) return
        if (DeliveryConfig(this).token() == null) return
        val panel = proxy ?: PanelProxy(applicationContext).also { proxy = it }
        server = panel.origin
        webState = Web.Opening
        opening = lifecycleScope.launch {
            when (val result = withContext(Dispatchers.IO) { panel.open() }) {
                PanelProxy.Opened.Yes -> {
                    CookieManager.getInstance().apply {
                        setAcceptCookie(true)
                        setCookie(panel.origin, "${PanelProxy.KEY_COOKIE}=${panel.key}; Path=/; HttpOnly")
                        flush()
                    }
                    loadFailed = false
                    web.loadUrl("${panel.origin}/panel/${PanelLinks.safeHash(hash) ?: ""}")
                }
                is PanelProxy.Opened.Refused -> webState = Web.Unavailable(result.status)
                PanelProxy.Opened.Unreachable -> webState = Web.Unavailable(null)
            }
        }
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

    /** The panel moved to another address: the bar follows, and each place remembers its last address. */
    private fun followAddress(url: String) {
        if (webState != Web.Open) return
        val hash = runCatching { Uri.parse(url).fragment }.getOrNull()?.let { "#$it" } ?: return
        when (PanelLinks.place(hash)) {
            "activity" -> { activityHash = hash; if (place.web) place = Place.ACTIVITY }
            "settings" -> { settingsHash = hash; if (place.web) place = Place.SETTINGS }
            // A link in the panel to Today opens the app's own.
            else -> if (place.web) place = Place.TODAY
        }
    }

    private val habitEditing = HabitEditing(
        rename = { id, name -> edit { it.change(id, name = name) } },
        weekdays = { id, days -> edit { it.change(id, weekdays = days) } },
        archive = { id, archived -> edit { it.change(id, archived = archived) } },
        add = { name -> edit { it.addHabit(name) } },
        reasons = { reasons -> edit { it.reasons(reasons) } },
    )

    @Composable
    private fun App() {
        val colors = panelColors()
        val zone = ZoneId.systemDefault()
        var now by remember { mutableStateOf(Instant.now()) }
        val today = remember(saved, now) { HomeState.today(saved, now, zone) }
        // Seconds while a door waits, so its countdown moves; else every half minute.
        LaunchedEffect(today.door != null) {
            while (true) {
                delay(if (today.door != null) 1_000L else 30_000L)
                now = Instant.now()
            }
        }
        BackHandler(enabled = place != Place.TODAY) {
            if (place.web && canGoBack) web.goBack() else place = Place.TODAY
        }
        Column(Modifier.fillMaxSize().background(colors.bg).windowInsetsPadding(WindowInsets.safeDrawing)) {
            if (!paired) {
                NotPaired()
                return@Column
            }
            attention?.let { step ->
                // One line, only while setup needs something; it opens that step.
                Row(Modifier.tapRow("Setup needs attention. Fix") { openSetup(step) }.background(colors.soft).padding(horizontal = 16.dp, vertical = 8.dp),
                    verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                    PText("Setup needs attention · ", size = 14.sp)
                    PText("Fix", size = 14.sp, weight = FontWeight.Bold)
                }
            }
            val stale = if (reachable == false) TodayText.saved(savedAt, now, zone) else null
            Box(Modifier.weight(1f).fillMaxWidth()) {
                // The panel stays loaded behind the native places, so it comes back where it was.
                AndroidView(factory = { web }, modifier = Modifier.fillMaxSize(),
                    update = { it.visibility = if (place.web && webState == Web.Open) View.VISIBLE else View.INVISIBLE })
                when (place) {
                    Place.TODAY -> TodayScreen(
                        state = today,
                        dateLabel = DateTimeFormatter.ofPattern("EEE d MMM", Locale.UK).format(now.atZone(zone)),
                        stale = stale,
                        notice = notice,
                        doorNotice = doorNotice,
                        zone = zone,
                        doorBusy = doorBusy,
                        onTick = { id, done -> if (today.ticksOpen) tick(id, today.day, done) },
                        onCancelDoor = ::cancelDoor,
                    )
                    Place.HABITS -> HabitsScreen(
                        state = remember(saved, now) { HomeState.habits(saved, now, zone) },
                        stale = stale,
                        notice = editNotice,
                        editing = habitEditing,
                    )
                    Place.ACTIVITY, Place.SETTINGS -> when (val state = webState) {
                        Web.Open -> Unit
                        is Web.Unavailable -> PanelUnavailable(false, state.refusedWith, onRetry = { openPanel(hashFor(place)) },
                            onPhoneSettings = { startActivity(SettingsActivity.intent(this@MainActivity, null)) })
                        Web.Opening, Web.Closed -> PanelUnavailable(true, null, onRetry = {}, onPhoneSettings = {})
                    }
                }
            }
            PlaceBar(place) { select(it) }
        }
    }

    @Composable
    private fun NotPaired() {
        Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center) {
            if (removed) {
                PText("This phone was removed from Telemetry", size = 20.sp, weight = FontWeight.Bold)
                Small("It was removed in the panel. Connect it again with a new setup code; what it recorded meanwhile is kept.", Modifier.padding(top = 8.dp, bottom = 16.dp))
            } else {
                PText("This phone isn't connected yet", size = 20.sp, weight = FontWeight.Bold)
                Small("Finish setting it up to open Telemetry here.", Modifier.padding(top = 8.dp, bottom = 16.dp))
            }
            Row(Modifier.tapRow("Set up") { openSetup(SetupStep.PAIR) }) { PText("Set up ›", size = 15.sp) }
            Row(Modifier.tapRow("Phone settings") { startActivity(SettingsActivity.intent(this@MainActivity, null)) }) { PText("Phone settings ›", size = 15.sp) }
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun buildWebView(): WebView = WebView(this).apply {
        setBackgroundColor(getColor(R.color.background))
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.userAgentString = PanelLinks.userAgent(settings.userAgentString, BuildConfig.VERSION_NAME)
        webViewClient = PanelClient()
        visibility = View.INVISIBLE
    }

    private inner class PanelClient : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
            follow(request.url.toString(), request.isForMainFrame)

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (!request.isForMainFrame) return
            loadFailed = true
            webState = Web.Unavailable(null)
        }

        // A proxy or relay that answers for a server it can't reach.
        override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, response: WebResourceResponse) {
            if (!request.isForMainFrame || response.statusCode < 500) return
            loadFailed = true
            webState = Web.Unavailable(null)
        }

        override fun onPageFinished(view: WebView, url: String) {
            if (!loadFailed && webState == Web.Opening) webState = Web.Open
        }

        override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
            canGoBack = view.canGoBack()
            followAddress(url)
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
