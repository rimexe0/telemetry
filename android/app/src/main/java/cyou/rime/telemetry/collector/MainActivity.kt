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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant

/** What the home screen shows. */
data class HomeState(
    val mode: String = "",
    val title: String = "",
    val taskState: String? = null,
    val next: String? = null,
    val resume: String? = null,
    val reason: String = "",
    val footer: String = "",
    val needsSetup: Boolean = false,
    val needsBlocking: Boolean = false,
    val insights: List<Insight> = emptyList(),
)

/**
 * The owner's everyday screen: what to do now, why something is closed, where
 * to return, and a few facts about today. The planner opens from the facts;
 * collection and pairing controls live in [SettingsActivity].
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
                    onSetup = ::openSetup,
                    onBlocking = { startActivity(Intent(android.provider.Settings.ACTION_ACCESSIBILITY_SETTINGS)) },
                )
            }
        }
        showCached()
        showCachedInsights()
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

    private suspend fun refresh() {
        val token = DeliveryConfig(this).token()
        state = state.copy(
            needsSetup = SetupActivity.needsSetup(this),
            needsBlocking = token != null && !PolicyEvents.blockingEnabled(this),
        )
        if (token == null) {
            if (!showCached()) {
                state = state.copy(mode = "NOT SET UP", title = "This phone is not paired yet", taskState = null, next = null, resume = null,
                    reason = "Finish setting it up to see your current task and follow your plan.", footer = "")
            }
            return
        }
        val body = withContext(Dispatchers.IO) { NowCache.refresh(this@MainActivity) }
        if (body != null) {
            render(NowModel.parse(body, Instant.now(), Instant.now(), offline = false))
        } else if (!showCached()) {
            state = state.copy(mode = "OFFLINE", title = "No plan information yet", taskState = null, next = null, resume = null,
                reason = "The server could not be reached and nothing is cached. Nothing is assumed.", footer = "")
        }
        withContext(Dispatchers.IO) { Insights.refresh(this@MainActivity) }
        showCachedInsights()
    }

    /** Shows the cached state, following its timeline while offline. */
    private fun showCached(): Boolean {
        val (json, fetchedAt) = NowCache.read(this) ?: return false
        val now = Instant.now()
        if (!NowModel.timelineCovers(json, now)) {
            state = state.copy(mode = "OFFLINE", title = "Plan information is out of date", taskState = null, next = null, resume = null,
                reason = "Last update ${NowModel.ageText(fetchedAt, now)}. Nothing is assumed until the server is reachable.", footer = "")
            return true
        }
        render(NowModel.parse(json, fetchedAt, now, offline = true))
        return true
    }

    /** Today's facts stay on screen offline; yesterday's are dropped. */
    private fun showCachedInsights() {
        val entry = Insights.read(this)
        val fresh = entry != null && Insights.isToday(entry.json, Instant.now())
        state = state.copy(insights = if (fresh) runCatching { Insights.parse(entry!!.json, Insights.appName(this)) }.getOrDefault(emptyList()) else emptyList())
    }

    private fun render(view: NowView) {
        val age = NowModel.ageText(view.asOf, Instant.now())
        state = state.copy(
            mode = view.modeLabel.uppercase(),
            title = view.taskTitle ?: if (view.planExists) "Nothing scheduled right now" else "No plan for today",
            taskState = view.taskState,
            next = view.nextAction,
            resume = view.resumeMarker,
            reason = view.reason,
            footer = if (view.offline) "Offline: following the plan cached $age." else "Updated $age.",
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HomeScreen(state: HomeState, onSettings: () -> Unit, onPlanner: () -> Unit, onSetup: () -> Unit, onBlocking: () -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(state.mode, style = MaterialTheme.typography.labelLarge, letterSpacing = 2.sp, color = MaterialTheme.colorScheme.onSurfaceVariant) },
                actions = {
                    IconButton(onClick = onSettings) { Icon(painterResource(R.drawable.ic_settings), contentDescription = "Settings") }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 20.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text(state.title, style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
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
            if (state.reason.isNotBlank()) Text(state.reason, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (state.needsSetup) Prompt("This phone is not fully set up.", "Finish setup", onSetup)
            if (state.needsBlocking) Prompt("Blocking is switched off on this phone.", "Turn on", onBlocking)
            Today(state.insights, onPlanner)
            if (state.footer.isNotBlank()) Text(state.footer, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
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

/** Today's facts; the whole card opens the planner for the details. */
@Composable
private fun Today(insights: List<Insight>, onPlanner: () -> Unit) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = Modifier.fillMaxWidth().clickable(onClick = onPlanner),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("TODAY", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (insights.isEmpty()) Text("Today's numbers show up here when the phone has reached the server.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            insights.forEachIndexed { index, insight ->
                if (index > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Column {
                    Text(insight.text, style = MaterialTheme.typography.titleMedium)
                    insight.detail?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                }
            }
            Text("Plan and details  ›", style = MaterialTheme.typography.labelLarge)
        }
    }
}
