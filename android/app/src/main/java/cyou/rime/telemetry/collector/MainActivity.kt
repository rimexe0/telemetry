package cyou.rime.telemetry.collector

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
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
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import cyou.rime.telemetry.collector.ui.ActivityScreen
import cyou.rime.telemetry.collector.ui.HabitEditing
import cyou.rime.telemetry.collector.ui.SettingsScreen
import cyou.rime.telemetry.collector.ui.HabitsScreen
import cyou.rime.telemetry.collector.ui.RulePicker
import cyou.rime.telemetry.collector.ui.PText
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
 * The app: four places along the bottom, as in the panel (owner 2026-11-26),
 * all drawn natively (owner 2026-11-27). Each shows at once from answers
 * saved on the phone and keeps working while the server is down; changes
 * are saved on the phone first and sent when the server can be reached
 * ([HabitTicks], [HabitEdits], [SettingsQueue]). Activity and Settings
 * reach the owner's routes with this phone's panel session ([OwnerApi]).
 * Each place keeps its state in memory, so switching never reloads it.
 * Setup on first run and the setup strip stay as they were.
 */
class MainActivity : ComponentActivity() {
    private companion object {
        const val REFRESH_MS = 60_000L
        /** The year's heatmap is fetched at most this often. */
        val YEAR_EVERY: Duration = Duration.ofMinutes(10)
    }

    /** Activity's answers, kept for the life of the screen so a tab switch never reloads them. */
    private lateinit var activity: ActivityData
    /** Settings' values and the changes waiting to be sent, kept for the life of the screen. */
    private lateinit var settings: SettingsData

    private var place by mutableStateOf(Place.TODAY)
    // Kept in memory for the life of the app, so each place shows at once on every visit.
    private var saved by mutableStateOf(HomeMemory.memory.current ?: Saved(null, null, null, null, emptyList()))
    /** Null until the first try; false while the server can't be reached. */
    private var reachable by mutableStateOf<Boolean?>(null)
    private var savedAt by mutableStateOf<Instant?>(null)
    private var paired by mutableStateOf(true)
    private var removed by mutableStateOf(false)
    private var attention by mutableStateOf<SetupStep?>(null)
    private var notice by mutableStateOf<String?>(null)
    /** Why an edit on the Habits screen was refused, once. */
    private var editNotice by mutableStateOf<String?>(null)
    private var doorNotice by mutableStateOf<String?>(null)
    private var doorBusy by mutableStateOf(false)
    /** The habit whose rule is being picked on the Habits place, or null. */
    private var pickingRule by mutableStateOf<Long?>(null)

    private var yearFetchedAt: Instant = Instant.EPOCH
    /** One talk with the server at a time, so a fetch never lands over an edit just sent. */
    private val talking = Mutex()
    private var sending: Job? = null
    private var sendAgain = false

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT), SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT))
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) SetupLink.apply(this, intent)
        activity = ActivityData(applicationContext, lifecycleScope)
        activity.prune()
        settings = SettingsData(applicationContext, lifecycleScope)
        // What the panel's web view kept on the phone is no longer used.
        if (savedInstanceState == null) lifecycleScope.launch(Dispatchers.IO) {
            runCatching { java.io.File(cacheDir, "panel-files").deleteRecursively() }
            runCatching { java.io.File(dataDir, "app_webview").deleteRecursively() }
            runCatching { deleteSharedPreferences("telemetry-panel-files") }
        }
        DeliveryConfig(this).let { config ->
            paired = config.token() != null
            removed = config.removed
        }
        // The saved copy at once; the server is asked after.
        reload()
        setContent { TelemetryTheme { App() } }
        CollectionStarter.ensureRunning(this)
        if (savedInstanceState == null && SetupActivity.needsSetup(this)) openSetup(null)

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

    // A setup link or a notification while the app is open.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        SetupLink.apply(this, intent)
        // The widget's head opens Today.
        if (intent.action == HabitWidgets.OPEN_TODAY) place = Place.TODAY
        if (SetupActivity.needsSetup(this)) openSetup(null)
    }

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
            // Read here, off the main thread, so reload() finds it in memory.
            runCatching { HomeMemory.read(this@MainActivity) }
            fresh
        } }
        reload()
        if (place == Place.ACTIVITY) activity.show()
        // Settings' values stay fresh, and a change made offline goes once the server can be reached.
        if (place == Place.SETTINGS || settings.waiting.isNotEmpty()) settings.show()
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

    private fun select(next: Place) {
        val again = next == place
        place = next
        if (next == Place.ACTIVITY) activity.show()
        if (next == Place.SETTINGS) {
            // Tapping Settings again goes back to its list.
            if (again && settings.page != null) settings.open(null) else settings.show()
        }
    }

    private fun openSetup(step: SetupStep?) = startActivity(SetupActivity.intent(this, step))

    private val habitEditing = HabitEditing(
        rename = { id, name -> edit { it.change(id, name = name) } },
        weekdays = { id, days -> edit { it.change(id, weekdays = days) } },
        archive = { id, archived -> edit { it.change(id, archived = archived) } },
        add = { name -> edit { it.addHabit(name) } },
        reasons = { reasons -> edit { it.reasons(reasons) } },
        pickRule = { id -> pickingRule = id },
        clearRule = { id -> edit { it.change(id, clearRule = true) } },
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
        // Back from a Settings page goes to its list (SettingsScreen); from a place, to Today.
        BackHandler(enabled = place != Place.TODAY) { place = Place.TODAY }
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
            // Each place keeps its scroll and its open parts while another is shown.
            val kept = androidx.compose.runtime.saveable.rememberSaveableStateHolder()
            Box(Modifier.weight(1f).fillMaxWidth()) {
                kept.SaveableStateProvider(place.name) { when (place) {
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
                        ruleLines = remember(today, now) { ruleLines(today.habits, HabitRuleWatch.latest(this@MainActivity), now, zone) },
                    )
                    Place.HABITS -> {
                        val habitsState = remember(saved, now) { HomeState.habits(saved, now, zone) }
                        val picking = pickingRule?.let { id -> habitsState.habits?.firstOrNull { it.id == id } }
                        if (picking != null) {
                            RulePicker(picking.name, picking.rule, remember(picking.id) { HabitRuleWatch.latest(this@MainActivity) },
                                onSave = { rule ->
                                    pickingRule = null
                                    edit { it.change(picking.id, rule = rule) }
                                },
                                onCancel = { pickingRule = null })
                        } else HabitsScreen(
                            state = habitsState,
                            stale = stale,
                            notice = editNotice,
                            editing = habitEditing,
                        )
                    }
                    Place.ACTIVITY -> ActivityScreen(activity, now, zone)
                    Place.SETTINGS -> SettingsScreen(settings, now, zone) { section -> startActivity(SettingsActivity.intent(this@MainActivity, section)) }
                } }
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
}
