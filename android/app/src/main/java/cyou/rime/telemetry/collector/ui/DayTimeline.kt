package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import cyou.rime.telemetry.collector.AppNames
import cyou.rime.telemetry.collector.DayChunk
import cyou.rime.telemetry.collector.DayModel
import cyou.rime.telemetry.collector.DayView
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

val DistractionColor = Color(0xFFE5484D)
val TaskColor = Color(0xFF2F8F5B)
val OtherColor = Color(0xFF55544F)

fun kindColor(kind: String): Color = when (kind) {
    DayModel.DISTRACTION -> DistractionColor
    DayModel.TASK -> TaskColor
    else -> OtherColor
}

private val HourHeight = 64.dp
private val HoursWidth = 40.dp
private val LabelFrom = 18.dp

/**
 * The day top to bottom: planned blocks, then what the Mac and the phone
 * recorded. Red is time on distractions, green is time on the planned task,
 * grey is anything else, and empty means nothing was recorded. A stretch tall enough names its main app.
 */
@Composable
fun DayTimeline(view: DayView, now: Instant, names: AppNames, modifier: Modifier = Modifier, zone: ZoneId = ZoneId.systemDefault(), onChunk: (DayChunk) -> Unit) {
    val (start, end) = DayModel.window(view, zone)
    fun down(at: Instant): Dp = HourHeight * (Duration.between(start, at).toMillis() / 3_600_000f)
    val hours = generateSequence(start) { it.plusSeconds(3600) }.takeWhile { !it.isAfter(end) }.toList()
    val line = MaterialTheme.colorScheme.outlineVariant
    val marker = MaterialTheme.colorScheme.onSurface
    Column(modifier) {
        Row {
            Spacer(Modifier.width(HoursWidth))
            listOf("plan", "mac", "phone").forEach {
                Text(it, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f).padding(start = 4.dp, bottom = 4.dp))
            }
        }
        Row(
            // A block that runs past the day is cut at midnight, the timeline's end.
            Modifier.fillMaxWidth().height(down(end)).clipToBounds().drawWithContent {
                hours.forEach { drawLine(line, Offset(HoursWidth.toPx(), down(it).toPx()), Offset(size.width, down(it).toPx())) }
                drawContent()
                if (!now.isBefore(start) && !now.isAfter(end)) {
                    drawLine(marker, Offset(HoursWidth.toPx(), down(now).toPx()), Offset(size.width, down(now).toPx()), strokeWidth = 2.dp.toPx())
                }
            },
        ) {
            Box(Modifier.width(HoursWidth).fillMaxHeight()) {
                hours.dropLast(1).forEach {
                    Text(String.format("%02d:00", it.atZone(zone).hour), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.offset(y = down(it) + 2.dp))
                }
            }
            Lane {
                view.blocks.forEach { block ->
                    // A block that runs past the day is drawn to the day's end with its
                    // border closed, and says when it really ends.
                    val runsOn = block.end.isAfter(end)
                    val shownEnd = if (runsOn) end else block.end
                    val until = block.end.atZone(zone)
                    val footer = if (!runsOn) null else {
                        val day = if (until.toLocalDate() == end.atZone(zone).toLocalDate()) "tomorrow" else until.dayOfWeek.name.take(3).lowercase().replaceFirstChar { it.uppercase() }
                        "until $day ${String.format("%02d:%02d", until.hour, until.minute)}"
                    }
                    Stretch(down(block.start), down(shownEnd) - down(block.start), MaterialTheme.colorScheme.surfaceContainerHighest, block.title, MaterialTheme.colorScheme.onSurface,
                        Modifier.border(1.dp, MaterialTheme.colorScheme.onSurfaceVariant), footer)
                }
            }
            listOf("mac", "phone").forEach { device ->
                Lane {
                    view.chunks.filter { it.device == device }.forEach { chunk ->
                        val top = chunk.items.firstOrNull()
                        Stretch(down(chunk.start), down(chunk.end) - down(chunk.start), kindColor(chunk.kind),
                            (chunk.task ?: top?.let { names.of(it.name) })?.let { "$it ${DayModel.clock(chunk.activeMinutes)}" }.orEmpty(), Color.White,
                            Modifier.clickable { onChunk(chunk) })
                    }
                }
            }
        }
    }
}

@Composable
private fun RowScope.Lane(content: @Composable BoxScope.() -> Unit) {
    Box(Modifier.weight(1f).fillMaxHeight().padding(horizontal = 2.dp), content = content)
}

@Composable
private fun Stretch(top: Dp, height: Dp, color: Color, label: String, labelColor: Color, modifier: Modifier, footer: String? = null) {
    Box(Modifier.offset(y = top).fillMaxWidth().height(height.coerceAtLeast(2.dp)).background(color).then(modifier)) {
        if (height >= LabelFrom) {
            Text(label, style = MaterialTheme.typography.labelSmall, color = labelColor, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 4.dp, vertical = 1.dp))
        }
        if (footer != null && height >= LabelFrom * 2) {
            Text(footer, style = MaterialTheme.typography.labelSmall, color = labelColor, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.align(Alignment.BottomStart).padding(horizontal = 4.dp, vertical = 2.dp))
        }
    }
}
