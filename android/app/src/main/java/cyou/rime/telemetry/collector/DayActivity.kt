package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import android.graphics.Color as AndroidColor
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.ExperimentalMaterial3Api
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
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import cyou.rime.telemetry.collector.ui.DistractionColor
import cyou.rime.telemetry.collector.ui.OtherColor
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/**
 * Where the time went, grouped: every app and site with its total, biggest
 * first, for the whole day or for one stretch of the timeline. Tapping a
 * row lists its visits.
 */
class DayActivity : ComponentActivity() {
    companion object {
        private const val DEVICE = "device"
        private const val START = "start"
        private const val DATE = "date"

        /** A whole day (YYYY-MM-DD), or one stretch of it. */
        fun intent(context: Context, date: String, chunk: DayChunk?): Intent = Intent(context, DayActivity::class.java).apply {
            putExtra(DATE, date)
            if (chunk != null) {
                putExtra(DEVICE, chunk.device)
                putExtra(START, chunk.start.toEpochMilli())
            }
        }
    }

    private var view by mutableStateOf<DayView?>(null)
    private var message by mutableStateOf("Loading…")

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(AndroidColor.TRANSPARENT), SystemBarStyle.dark(AndroidColor.TRANSPARENT))
        super.onCreate(savedInstanceState)
        // The cached day shows the totals at once; the visits arrive with the full answer.
        view = DayCache.read(this, date)
        setContent { TelemetryTheme { Screen() } }
        lifecycleScope.launch {
            val full = withContext(Dispatchers.IO) { DayCache.details(this@DayActivity, date) }
            if (full != null) view = full
            message = if (view == null) "The server could not be reached." else ""
        }
    }

    private val zone = ZoneId.systemDefault()

    private val date: String by lazy { intent.getStringExtra(DATE) ?: DayModel.today(Instant.now()) }

    private fun time(at: Instant) = String.format("%02d:%02d", at.atZone(zone).hour, at.atZone(zone).minute)

    /** The chunks in scope: the one the timeline opened, or all of them. */
    private fun scope(day: DayView): List<DayChunk> {
        val device = intent.getStringExtra(DEVICE) ?: return day.chunks
        val start = intent.getLongExtra(START, 0)
        // A chunk that grew since the tap still starts at the same moment.
        return day.chunks.filter { it.device == device && it.start.toEpochMilli() == start }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    private fun Screen() {
        val names = remember { AppNames(this) }
        val day = view
        val chunks = day?.let(::scope).orEmpty()
        val whole = intent.getStringExtra(DEVICE) == null
        Scaffold(topBar = {
            TopAppBar(
                title = { Text(if (whole || chunks.isEmpty()) DayModel.dayLabel(date, Instant.now()) else "${time(chunks.first().start)}–${time(chunks.last().end)} · ${DayModel.deviceName(chunks.first().device)}") },
                navigationIcon = { IconButton(onClick = ::finish) { Icon(painterResource(R.drawable.ic_back), contentDescription = "Back") } },
            )
        }) { padding ->
            if (day == null) {
                Text(message, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(padding).padding(20.dp))
                return@Scaffold
            }
            val items = remember(day) { DayModel.grouped(chunks) }
            val segments = remember(day) { chunks.flatMap { it.segments } }
            var open by remember { mutableStateOf<DayItem?>(null) }
            LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                items(items) { item ->
                    val visits = if (open == item) segments.filter { it.device == item.device && it.name == item.name && it.detail == item.detail } else emptyList()
                    Item(item, names, whole, open == item, visits) { open = if (open == item) null else item }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
                if (whole) items(day.gaps) { gap ->
                    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp)) {
                        Text("No data · ${DayModel.deviceName(gap.device)}", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
                        Text("${time(gap.start)}–${time(gap.end)}", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
        }
    }

    @Composable
    private fun Item(item: DayItem, names: AppNames, showDevice: Boolean, open: Boolean, visits: List<DaySegment>, onClick: () -> Unit) {
        Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min).clickable(onClick = onClick)) {
            Box(Modifier.width(4.dp).fillMaxHeight().background(if (item.label == DayModel.DISTRACTION) DistractionColor else OtherColor))
            Column(Modifier.padding(horizontal = 12.dp, vertical = 10.dp)) {
                Row {
                    Text(names.of(item.name), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    Text(DayModel.clock(item.minutes), fontWeight = FontWeight.Bold, modifier = Modifier.padding(start = 12.dp))
                }
                val under = listOfNotNull(item.detail, DayModel.deviceName(item.device).takeIf { showDevice }, "${item.visits} ${if (item.visits == 1) "visit" else "visits"}")
                Text(under.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (open && visits.isEmpty()) Text("The visits are only available online.", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 4.dp))
                visits.forEach { visit ->
                    Row(Modifier.padding(top = 4.dp)) {
                        Text(time(visit.start), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.width(48.dp))
                        Text(visit.title?.takeIf { it != visit.name } ?: "", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                        Text(DayModel.clock(Duration.between(visit.start, visit.end).toMillis() / 60_000.0), style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(start = 8.dp))
                    }
                }
            }
        }
    }
}
