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
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
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
    val needsSetup: Boolean = false,
    val needsBlocking: Boolean = false,
    val day: DayView? = null,
    val followUps: List<FollowUp> = emptyList(),
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

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(AndroidColor.TRANSPARENT), SystemBarStyle.dark(AndroidColor.TRANSPARENT))
        super.onCreate(savedInstanceState)
        setContent {
            TelemetryTheme {
                HomeScreen(
                    state = state,
                    onSettings = { startActivity(Intent(this, SettingsActivity::class.java)) },
                    onPlanner = { startActivity(Intent(this, PanelActivity::class.java)) },
                    onDay = { chunk -> startActivity(DayActivity.intent(this, chunk)) },
                    onSetup = ::openSetup,
                    onBlocking = { startActivity(Intent(android.provider.Settings.ACTION_ACCESSIBILITY_SETTINGS)) },
                    onDone = ::done,
                )
            }
        }
        showCached()
        showFollowUps()
        showDay(DayCache.read(this))
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

    private fun openSetup() = startActivity(Intent(this, SetupActivity::class.java))

    private fun showFollowUps() {
        state = state.copy(followUps = FollowUpWatch.open(this, NowCache.read(this)?.json))
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
        state = state.copy(
            needsSetup = SetupActivity.needsSetup(this),
            needsBlocking = token != null && !PolicyEvents.blockingEnabled(this),
        )
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
        showDay(withContext(Dispatchers.IO) { DayCache.refresh(this@MainActivity) } ?: DayCache.read(this))
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

    /** Yesterday's answer is not shown as today. */
    private fun showDay(view: DayView?) {
        state = state.copy(day = view?.takeIf { DayModel.isToday(it, Instant.now()) })
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
private fun HomeScreen(state: HomeState, onSettings: () -> Unit, onPlanner: () -> Unit, onDay: (DayChunk?) -> Unit, onSetup: () -> Unit, onBlocking: () -> Unit,
    onDone: (FollowUp) -> Unit) {
    val context = LocalContext.current
    val names = remember { AppNames(context) }
    Scaffold(
        topBar = {
            TopAppBar(title = {}, actions = {
                IconButton(onClick = onSettings) { Icon(painterResource(R.drawable.ic_settings), contentDescription = "Settings") }
            })
        },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            FollowUps(state.followUps, onDone)
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
            if (state.needsSetup) Prompt("This phone is not fully set up.", "Finish setup", onSetup)
            if (state.needsBlocking) Prompt("Blocking is switched off on this phone.", "Turn on", onBlocking)
            state.day?.let { day ->
                Plan(day, onPlanner)
                Totals(day, Modifier.fillMaxWidth().clickable { onDay(null) })
                DayTimeline(day, Instant.now(), names, Modifier.fillMaxWidth(), onChunk = onDay)
            }
            state.footer?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

@Composable
private fun Prompt(text: String, action: String, onClick: () -> Unit) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 14.dp, end = 8.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(text, modifier = Modifier.weight(1f))
            FilledTonalButton(onClick = onClick) { Text(action) }
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

/** Today's blocks with the time spent inside each; opens the planner. */
@Composable
private fun Plan(day: DayView, onPlanner: () -> Unit) {
    val zone = ZoneId.systemDefault()
    fun time(at: Instant) = String.format("%02d:%02d", at.atZone(zone).hour, at.atZone(zone).minute)
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.fillMaxWidth().clickable(onClick = onPlanner),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (day.blocks.isEmpty()) Text("No plan today", color = MaterialTheme.colorScheme.onSurfaceVariant)
            day.blocks.forEach { block ->
                val current = block.status == "in_progress"
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("${time(block.start)}–${time(block.end)}", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(end = 12.dp))
                    Text(block.title, fontWeight = if (current) FontWeight.Bold else FontWeight.Normal, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    // Dim when the task has no rules: the number is then only time off distractions.
                    if (block.status != "upcoming") Text(DayModel.clock(block.inPlanMinutes), modifier = Modifier.padding(start = 12.dp),
                        color = if (block.tracked) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

@Composable
private fun Totals(day: DayView, modifier: Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        if (day.blocks.isNotEmpty()) Total("In plan", "${DayModel.clock(day.inPlanMinutes)} of ${DayModel.clock(day.plannedMinutes)}", null)
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
