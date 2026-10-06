package cyou.rime.telemetry.collector

import android.content.Intent
import android.graphics.Color as AndroidColor
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import cyou.rime.telemetry.collector.ui.DayTimeline
import cyou.rime.telemetry.collector.ui.DistractionColor
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/** What the home screen shows. */
data class HomeState(
    /** Set only when there is something to say: a task, a block reason, or a problem. */
    val title: String? = null,
    val taskState: String? = null,
    val next: String? = null,
    val resume: String? = null,
    val reason: String? = null,
    val footer: String? = null,
    /** Permissions and setup steps the phone is missing, named on the screen. */
    val missing: List<SetupStep> = emptyList(),
    /** Something is blocked now, so the emergency unlock is offered. */
    val blocking: Boolean = false,
    /** Set while an emergency unlock is open. */
    val emergencyUntil: Instant? = null,
    val followUps: List<FollowUp> = emptyList(),
    val focus: FocusView? = null,
)

/**
 * The owner's everyday screen: the current task when there is one, today's
 * plan with the time spent inside it, time on distractions, and the day as a
 * timeline. The plan opens the planner; the totals and each stretch of the
 * timeline open their details; collection and pairing controls live in [SettingsActivity].
 */
class MainActivity : ComponentActivity() {
    private companion object {
        const val REFRESH_MS = 60_000L
    }

    private var state by mutableStateOf(HomeState())

    /** Loaded days by date (YYYY-MM-DD); swiping asks for more. */
    private val days = mutableStateMapOf<String, DayView>()

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(AndroidColor.TRANSPARENT), SystemBarStyle.dark(AndroidColor.TRANSPARENT))
        super.onCreate(savedInstanceState)
        setContent {
            TelemetryTheme {
                HomeScreen(
                    state = state,
                    onSettings = { startActivity(Intent(this, SettingsActivity::class.java)) },
                    onPlanner = { startActivity(Intent(this, PanelActivity::class.java)) },
                    days = days,
                    onShowDay = ::loadDay,
                    onDay = { date, chunk -> startActivity(DayActivity.intent(this, date, chunk)) },
                    onSetup = ::openSetup,
                    onEmergency = ::emergency,
                    onDone = ::done,
                    onFocus = ::focus,
                )
            }
        }
        showCached()
        showFollowUps()
        DayModel.today(Instant.now()).let { today -> DayCache.read(this, today)?.let { days[today] = it } }
        if (savedInstanceState == null && SetupActivity.needsSetup(this)) openSetup()
        CollectionStarter.ensureRunning(this)

        lifecycleScope.launch {
            repeatOnLifecycle(Lifecycle.State.RESUMED) {
                while (true) {
                    refresh()
                    delay(REFRESH_MS)
                }
            }
        }
    }

    private fun openSetup(step: SetupStep? = null) = startActivity(SetupActivity.intent(this, step))

    /** Whether something is blocked now, and any emergency unlock in force. */
    private fun showBlocking() {
        val json = NowCache.read(this)?.json
        val until = Emergency.until(this)?.takeIf { it.isAfter(Instant.now()) }
        state = state.copy(emergencyUntil = until, blocking = until == null && json != null && BlockPolicy.current(this, json)?.active == true)
    }

    private fun emergency() {
        lifecycleScope.launch {
            withContext(Dispatchers.IO) { Emergency.start(this@MainActivity) }
            showBlocking()
        }
    }

    private fun showFollowUps() {
        val json = NowCache.read(this)?.json
        state = state.copy(followUps = FollowUpWatch.open(this, json), focus = json?.let { runCatching { TrackingModel.parse(it).focus }.getOrNull() })
    }

    /** Starts, pauses or ends a focus session, then shows the server's new state. */
    private fun focus(action: String, taskId: Long) {
        lifecycleScope.launch {
            withContext(Dispatchers.IO) {
                DeviceApi.post(this@MainActivity, "/v1/device/focus", org.json.JSONObject().put("action", action).put("task_id", taskId).toString())
                NowCache.refresh(this@MainActivity)
            }
            showCached()
            showFollowUps()
        }
    }

    private fun done(followUp: FollowUp) {
        state = state.copy(followUps = state.followUps - followUp)
        lifecycleScope.launch { withContext(Dispatchers.IO) { runCatching { FollowUpWatch.markDone(this@MainActivity, followUp.id) } } }
    }

    private fun problem(title: String, reason: String) {
        state = state.copy(title = title, taskState = null, next = null, resume = null, reason = reason, footer = null)
    }

    private suspend fun refresh() {
        val token = DeliveryConfig(this).token()
        state = state.copy(missing = SetupStep.missing(SetupActivity.syncDone(this)))
        showBlocking()
        if (token == null) {
            if (!showCached()) problem("This phone is not paired yet", "Finish setting it up to see your plan.")
            return
        }
        val body = withContext(Dispatchers.IO) { NowCache.refresh(this@MainActivity) }
        if (body != null) {
            render(NowModel.parse(body, Instant.now(), Instant.now(), offline = false))
        } else if (!showCached()) {
            problem("No plan information yet", "The server could not be reached and nothing is cached. Nothing is assumed.")
        }
        showFollowUps()
        val today = DayModel.today(Instant.now())
        withContext(Dispatchers.IO) { DayCache.refresh(this@MainActivity, today) }?.let { days[today] = it }
    }

    /** Shows the cached state, following its timeline while offline. */
    private fun showCached(): Boolean {
        val (json, fetchedAt) = NowCache.read(this) ?: return false
        val now = Instant.now()
        if (!NowModel.timelineCovers(json, now)) {
            problem("Plan information is out of date", "Last update ${NowModel.ageText(fetchedAt, now)}. Nothing is assumed until the server is reachable.")
            return true
        }
        render(NowModel.parse(json, fetchedAt, now, offline = true))
        return true
    }

    /** Shows a day from the cache at once, then from the server. */
    private fun loadDay(date: String) {
        lifecycleScope.launch {
            if (date !in days) DayCache.read(this@MainActivity, date)?.let { days[date] = it }
            withContext(Dispatchers.IO) { DayCache.refresh(this@MainActivity, date) }?.let { days[date] = it }
        }
    }

    private fun render(view: NowView) {
        state = state.copy(
            title = view.taskTitle,
            taskState = view.taskState,
            next = view.nextAction,
            resume = view.resumeMarker,
            // Free time needs no words; the plan below shows it.
            reason = view.reason.takeIf { view.mode != "leisure" && it.isNotBlank() },
            footer = if (view.offline) "Offline: following the plan cached ${NowModel.ageText(view.asOf, Instant.now())}." else null,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HomeScreen(state: HomeState, days: Map<String, DayView>, onShowDay: (String) -> Unit, onSettings: () -> Unit, onPlanner: () -> Unit,
    onDay: (String, DayChunk?) -> Unit, onSetup: (SetupStep) -> Unit, onEmergency: () -> Unit, onDone: (FollowUp) -> Unit, onFocus: (String, Long) -> Unit) {
    val context = LocalContext.current
    val names = remember { AppNames(context) }
    // Today is the last page; swiping right goes back a day at a time.
    val pager = rememberPagerState(initialPage = DAYS_BACK - 1) { DAYS_BACK }
    val today = LocalDate.now()
    fun dateOf(page: Int) = today.minusDays((DAYS_BACK - 1 - page).toLong()).toString()
    LaunchedEffect(pager.settledPage) { if (pager.settledPage != DAYS_BACK - 1) onShowDay(dateOf(pager.settledPage)) }
    Scaffold(
        topBar = {
            TopAppBar(title = {
                // Today needs no name; an earlier day says which one it is.
                if (pager.currentPage != DAYS_BACK - 1) Text(DayModel.dayLabel(dateOf(pager.currentPage), Instant.now()), style = MaterialTheme.typography.titleMedium)
            }, actions = {
                IconButton(onClick = onSettings) { Icon(painterResource(R.drawable.ic_settings), contentDescription = "Settings") }
            })
        },
    ) { padding ->
        HorizontalPager(pager, Modifier.fillMaxSize().padding(padding), beyondViewportPageCount = 0) { page ->
            val date = dateOf(page)
            val isToday = page == DAYS_BACK - 1
            Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 20.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                if (isToday) {
                    FollowUps(state.followUps, onDone)
                    state.focus?.let { Focus(it, onFocus) }
                    state.title?.let { Text(it, style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold) }
                    state.taskState?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    state.next?.let { Text("Next: $it", style = MaterialTheme.typography.titleMedium) }
                    state.resume?.let {
                        Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), modifier = Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(14.dp)) {
                                Text("RETURN HERE", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                Text(it, style = MaterialTheme.typography.bodyLarge)
                            }
                        }
                    }
                    state.reason?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    Missing(state.missing, onSetup)
                    EmergencyRow(state.blocking, state.emergencyUntil, onEmergency)
                }
                days[date]?.let { day ->
                    Plan(day, onPlanner, if (isToday && state.focus == null) onFocus else null, isToday)
                    Totals(day, Modifier.fillMaxWidth().clickable { onDay(date, null) })
                    DayTimeline(day, Instant.now(), names, Modifier.fillMaxWidth(), onChunk = { chunk -> onDay(date, chunk) })
                }
                if (isToday) state.footer?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
        }
    }
}

/** How many days the home screen can swipe back through. */
private const val DAYS_BACK = 60

/** While something is blocked, the way to open everything for a few minutes; during that, until when. */
@Composable
private fun EmergencyRow(blocking: Boolean, until: Instant?, onEmergency: () -> Unit) {
    if (until != null) {
        val at = until.atZone(ZoneId.systemDefault())
        Text(String.format("Everything is open until %02d:%02d.", at.hour, at.minute), color = DistractionColor)
    } else if (blocking) {
        TextButton(onClick = onEmergency, contentPadding = PaddingValues(0.dp)) {
            Text("Emergency: open everything for ${Emergency.LENGTH.toMinutes()} min", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** Each thing the phone is missing, with the way to fix it; nothing when all is in place. */
@Composable
private fun Missing(missing: List<SetupStep>, onFix: (SetupStep) -> Unit) {
    if (missing.isEmpty()) return
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(start = 14.dp, end = 8.dp, top = 4.dp, bottom = 4.dp)) {
            missing.forEach { step ->
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 2.dp)) {
                    Text(step.missing.orEmpty(), color = DistractionColor, modifier = Modifier.weight(1f))
                    FilledTonalButton(onClick = { onFix(step) }) { Text("Fix") }
                }
            }
        }
    }
}

/** Things waiting on the owner: due ones first with Done, then what is coming. */
@Composable
private fun FollowUps(followUps: List<FollowUp>, onDone: (FollowUp) -> Unit) {
    if (followUps.isEmpty()) return
    val zone = ZoneId.systemDefault()
    val now = Instant.now()
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(start = 14.dp, end = 8.dp, top = 4.dp, bottom = 4.dp)) {
            followUps.forEach { followUp ->
                val due = !now.isBefore(followUp.dueAt)
                val at = followUp.dueAt.atZone(zone)
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 2.dp)) {
                    Text(String.format("%02d:%02d", at.hour, at.minute), color = if (due) DistractionColor else MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(end = 12.dp))
                    Text(followUp.title, fontWeight = if (due) FontWeight.Bold else FontWeight.Normal, modifier = Modifier.weight(1f))
                    FilledTonalButton(onClick = { onDone(followUp) }) { Text("Done") }
                }
            }
        }
    }
}

/** The running focus session: the one task, how it stands, and the way to pause or end it. */
@Composable
private fun Focus(focus: FocusView, onFocus: (String, Long) -> Unit) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(start = 14.dp, end = 8.dp, top = 12.dp, bottom = 6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("FOCUS", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(focus.task, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            Text(TrackingModel.focusLine(focus, Instant.now()), color = if (focus.on == "off" || focus.on == "unknown") DistractionColor else MaterialTheme.colorScheme.onSurfaceVariant)
            focus.nextAction?.let { Text("Next: $it") }
            Row {
                TextButton(onClick = { onFocus("pause", focus.taskId) }) { Text("Pause") }
                TextButton(onClick = { onFocus("done", focus.taskId) }) { Text("End") }
            }
        }
    }
}

/** Today's blocks and tasks with the time spent on each; opens the planner. */
@Composable
private fun Plan(day: DayView, onPlanner: () -> Unit, onFocus: ((String, Long) -> Unit)?, isToday: Boolean) {
    val zone = ZoneId.systemDefault()
    fun time(at: Instant) = String.format("%02d:%02d", at.atZone(zone).hour, at.atZone(zone).minute)
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.fillMaxWidth().clickable(onClick = onPlanner),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (day.blocks.isEmpty() && day.tasks.none { isToday || it.minutes >= 1 || it.agentMinutes >= 1 }) {
                Text(if (isToday) "No plan today" else "No plan that day", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            day.blocks.forEach { block ->
                val current = block.status == "in_progress"
                Row(verticalAlignment = Alignment.CenterVertically) {
                    // A block that ends on another day shows only when it began.
                    val sameDay = block.end.atZone(zone).toLocalDate() == block.start.atZone(zone).toLocalDate()
                    Text(if (sameDay) "${time(block.start)}–${time(block.end)}" else "from ${time(block.start)}", style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(end = 12.dp))
                    Text(block.title, fontWeight = if (current) FontWeight.Bold else FontWeight.Normal, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    // Dim when the task has no rules: the number is then only time off distractions.
                    if (block.status != "upcoming" && !block.focusOnly) Text(DayModel.clock(block.inPlanMinutes), modifier = Modifier.padding(start = 12.dp),
                        color = if (block.tracked) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            // Tasks need no time: each shows the owner's own time on it today. A second
            // line appears only when its agent has something to say.
            // An earlier day lists only the tasks that got time that day.
            day.tasks.filter { isToday || it.minutes >= 1 || it.agentMinutes >= 1 }.forEach { task ->
                Column {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(task.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                        if (onFocus != null) Text("Focus", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.clickable { onFocus("start", task.id) }.padding(horizontal = 10.dp, vertical = 4.dp))
                        Text(if (task.tracked) DayModel.clock(task.minutes) else "—", modifier = Modifier.padding(start = 8.dp),
                            color = if (task.tracked) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    val turn = task.yourTurnSince
                    when {
                        turn != null -> Text("Your turn since ${time(turn)}", style = MaterialTheme.typography.bodySmall, color = DistractionColor)
                        task.agentMinutes >= 1 -> Text("agent ${DayModel.clock(task.agentMinutes)} · waited ${DayModel.clock(task.waitingMinutes)}",
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
    }
}

@Composable
private fun Totals(day: DayView, modifier: Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        if (day.blocks.isNotEmpty() || day.tasks.isNotEmpty()) {
            Total("On plan", DayModel.clock(day.inPlanMinutes) + if (day.plannedMinutes >= 1) " of ${DayModel.clock(day.plannedMinutes)}" else "", null)
        }
        // Breaks after task work: rest is away from screens; scrolling is not rest.
        if (day.restMinutes >= 1 || day.scrollingMinutes >= 1) {
            Total("Breaks", DayModel.clock(day.restMinutes) + " rest",
                if (day.scrollingMinutes >= 1) DayModel.clock(day.scrollingMinutes) + " scrolling" else null)
        }
        Total("Distractions", DayModel.clock(day.distractionMinutes),
            day.distractionByDevice.filter { it.second >= 1 }.sortedByDescending { it.second }
                .joinToString(" · ") { "${DayModel.deviceName(it.first)} ${DayModel.clock(it.second)}" }.ifBlank { null })
    }
}

@Composable
private fun Total(name: String, value: String, detail: String?) {
    Row(verticalAlignment = Alignment.Bottom) {
        Text(name, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.width(110.dp))
        Text(value, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
        detail?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(start = 12.dp, bottom = 3.dp)) }
    }
}
