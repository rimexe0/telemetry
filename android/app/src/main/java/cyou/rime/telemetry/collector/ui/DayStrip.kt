package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import cyou.rime.telemetry.collector.DayModel
import cyou.rime.telemetry.collector.DayView
import java.time.Instant
import java.time.ZoneId

val DistractionColor = Color(0xFFE5484D)
val OtherColor = Color(0xFF6F6E69)

private val lanes = listOf("plan", "mac", "phone")

/**
 * The day on one strip: planned blocks on top, then what each device
 * recorded. Red is time on distractions, grey is anything else, and empty
 * means nothing was recorded.
 */
@Composable
fun DayStrip(view: DayView, now: Instant, modifier: Modifier = Modifier, zone: ZoneId = ZoneId.systemDefault()) {
    val (start, end) = DayModel.window(view, zone)
    val span = (end.toEpochMilli() - start.toEpochMilli()).coerceAtLeast(1).toFloat()
    val track = MaterialTheme.colorScheme.surfaceContainerHighest
    val planColor = MaterialTheme.colorScheme.onSurface
    Column(modifier) {
        Row {
            Column(Modifier.width(44.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                lanes.forEach { Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.height(14.dp)) }
            }
            Canvas(Modifier.weight(1f).height(50.dp)) {
                val lane = 14.dp.toPx()
                val gap = 4.dp.toPx()
                fun bar(index: Int, from: Instant, to: Instant, color: Color) {
                    val left = ((from.toEpochMilli() - start.toEpochMilli()) / span).coerceIn(0f, 1f) * size.width
                    val right = ((to.toEpochMilli() - start.toEpochMilli()) / span).coerceIn(0f, 1f) * size.width
                    drawRect(color, Offset(left, index * (lane + gap)), Size((right - left).coerceAtLeast(1.5f), lane))
                }
                lanes.indices.forEach { drawRect(track, Offset(0f, it * (lane + gap)), Size(size.width, lane)) }
                view.blocks.forEach { bar(0, it.start, it.end, planColor) }
                view.chunks.forEach { bar(if (it.device == "mac") 1 else 2, it.start, it.end, if (it.distraction) DistractionColor else OtherColor) }
                val marker = ((now.toEpochMilli() - start.toEpochMilli()) / span)
                if (marker in 0f..1f) drawRect(planColor, Offset(marker * size.width, 0f), Size(1.dp.toPx(), size.height))
            }
        }
        Row(Modifier.fillMaxWidth().padding(start = 44.dp), horizontalArrangement = Arrangement.SpaceBetween) {
            val middle = Instant.ofEpochMilli((start.toEpochMilli() + end.toEpochMilli()) / 2)
            listOf(start, middle, end).forEach {
                Text(String.format("%02d:%02d", it.atZone(zone).hour, it.atZone(zone).minute), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}
