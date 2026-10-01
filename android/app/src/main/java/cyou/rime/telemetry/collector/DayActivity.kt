package cyou.rime.telemetry.collector

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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import cyou.rime.telemetry.collector.ui.DayStrip
import cyou.rime.telemetry.collector.ui.DistractionColor
import cyou.rime.telemetry.collector.ui.OtherColor
import cyou.rime.telemetry.collector.ui.TelemetryTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/** One row of the day: a chunk of use, or a stretch with no data. */
private sealed interface DayRow {
    val start: Instant

    data class Use(val chunk: DayChunk) : DayRow {
        override val start get() = chunk.start
    }

    data class Missing(val gap: DayGap) : DayRow {
        override val start get() = gap.start
    }
}

/**
 * Today in order, newest first. Each row is a chunk of continuous use with
 * its apps and sites totalled; tapping it lists every record behind it.
 */
class DayActivity : ComponentActivity() {
    private var view by mutableStateOf<DayView?>(null)
    private var message by mutableStateOf("Loading…")

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(AndroidColor.TRANSPARENT), SystemBarStyle.dark(AndroidColor.TRANSPARENT))
        super.onCreate(savedInstanceState)
        setContent { TelemetryTheme { Screen() } }
        lifecycleScope.launch {
            view = withContext(Dispatchers.IO) { DayCache.details(this@DayActivity) }
            // Offline: the cached day still lists the chunks, without their records.
            if (view == null) view = DayCache.read(this@DayActivity)?.takeIf { DayModel.isToday(it, Instant.now()) }
            message = if (view == null) "The server could not be reached." else ""
        }
    }

    @OptIn(ExperimentalMaterial3Api::class)
    @Composable
    private fun Screen() {
        val names = remember { AppNames(this) }
        Scaffold(topBar = {
            TopAppBar(
                title = { Text("Today") },
                navigationIcon = { IconButton(onClick = ::finish) { Icon(painterResource(R.drawable.ic_back), contentDescription = "Back") } },
            )
        }) { padding ->
            val day = view
            if (day == null) {
                Text(message, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(padding).padding(20.dp))
                return@Scaffold
            }
            val rows = remember(day) {
                (day.chunks.map { DayRow.Use(it) } + day.gaps.map { DayRow.Missing(it) }).sortedByDescending { it.start }
            }
            var open by remember { mutableStateOf<DayChunk?>(null) }
            LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                item { DayStrip(day, Instant.now(), Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) }
                items(rows) { row ->
                    when (row) {
                        is DayRow.Use -> Use(row.chunk, names, open == row.chunk) { open = if (open == row.chunk) null else row.chunk }
                        is DayRow.Missing -> Missing(row.gap)
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                }
            }
        }
    }

    private val zone = ZoneId.systemDefault()

    private fun time(at: Instant) = String.format("%02d:%02d", at.atZone(zone).hour, at.atZone(zone).minute)

    private fun seconds(at: Instant) = String.format("%02d:%02d:%02d", at.atZone(zone).hour, at.atZone(zone).minute, at.atZone(zone).second)

    @Composable
    private fun Use(chunk: DayChunk, names: AppNames, open: Boolean, onClick: () -> Unit) {
        Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min).clickable(onClick = onClick)) {
            Box(Modifier.width(4.dp).fillMaxHeight().background(if (chunk.distraction) DistractionColor else OtherColor))
            Column(Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
                Row {
                    Text("${time(chunk.start)}–${time(chunk.end)}", modifier = Modifier.weight(1f))
                    Text("${DayModel.deviceName(chunk.device)} · ${DayModel.clock(chunk.activeMinutes)}", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Text(chunk.items.joinToString(" · ") { "${names.of(it.name)} ${DayModel.clock(it.minutes)}" },
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = if (open) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                if (open) {
                    if (chunk.segments.isEmpty()) Text("The records behind this are only available online.", style = MaterialTheme.typography.bodySmall)
                    chunk.segments.forEach { segment ->
                        Row(Modifier.padding(top = 4.dp)) {
                            Text(seconds(segment.start), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.width(64.dp))
                            Text(listOfNotNull(names.of(segment.name), segment.title?.takeIf { it != segment.name }).joinToString(" · "),
                                style = MaterialTheme.typography.bodySmall, color = if (segment.label == DayModel.DISTRACTION) DistractionColor else MaterialTheme.colorScheme.onSurface,
                                maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                            Text(DayModel.clock(Duration.between(segment.start, segment.end).toMillis() / 60_000.0), style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                }
            }
        }
    }

    @Composable
    private fun Missing(gap: DayGap) {
        Row(Modifier.fillMaxWidth().padding(start = 16.dp, end = 12.dp, top = 8.dp, bottom = 8.dp)) {
            Text("${time(gap.start)}–${time(gap.end)}", color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
            Text("${DayModel.deviceName(gap.device)} · no data", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
