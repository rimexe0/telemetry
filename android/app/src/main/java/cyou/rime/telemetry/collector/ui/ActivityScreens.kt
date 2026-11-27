package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.awaitHorizontalTouchSlopOrCancellation
import androidx.compose.foundation.gestures.horizontalDrag
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import cyou.rime.telemetry.collector.ActivityData
import cyou.rime.telemetry.collector.ActivitySection
import cyou.rime.telemetry.collector.ActivitySummary
import cyou.rime.telemetry.collector.ActivityText
import cyou.rime.telemetry.collector.BudgetText
import cyou.rime.telemetry.collector.DayModel
import cyou.rime.telemetry.collector.DayView
import cyou.rime.telemetry.collector.HealthDays
import cyou.rime.telemetry.collector.HealthDayView
import cyou.rime.telemetry.collector.LoadState
import cyou.rime.telemetry.collector.Showing
import cyou.rime.telemetry.collector.PlaceItem
import cyou.rime.telemetry.collector.Places
import cyou.rime.telemetry.collector.PlacesDay
import cyou.rime.telemetry.collector.Refresh
import cyou.rime.telemetry.collector.Shown
import cyou.rime.telemetry.collector.TimelineRow
import cyou.rime.telemetry.collector.TodayText
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** A page of the app: scrolls, with the panel's side gutter. */
@Composable
internal fun AppPage(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    val colors = panelColors()
    Column(modifier.fillMaxSize().background(colors.bg).verticalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, top = 16.dp, bottom = 24.dp)) {
        content()
    }
}

/** An underlined word that acts, as the panel's link buttons. */
@Composable
internal fun ActWord(text: String, enabled: Boolean = true, bold: Boolean = false, onClick: () -> Unit) {
    val colors = panelColors()
    Text(
        text,
        Modifier.heightIn(min = 44.dp).clickable(enabled = enabled, role = Role.Button, onClick = onClick).padding(horizontal = 4.dp, vertical = 12.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 13.sp, color = if (enabled) colors.fg else colors.muted, textDecoration = TextDecoration.Underline,
            fontWeight = if (bold) FontWeight.Bold else null),
    )
}

/**
 * The muted line over a saved answer: nothing while it is fresh or still
 * on its way; "saved 14:20" once the saved copy is old; the server's own
 * words when it refused; Today's words when it can't be reached.
 */
fun <T> staleLine(shown: Shown<T>, now: Instant, zone: ZoneId): String? {
    val at = shown.at
    return when (val refresh = shown.refresh) {
        Refresh.Unreachable -> TodayText.saved(at, now, zone)
        is Refresh.Refused -> refresh.message + (at?.let { " · saved ${BudgetText.clock(it, zone)}" } ?: "")
        Refresh.Fresh -> null
        Refresh.Never -> if (at != null && Duration.between(at, now) >= Duration.ofMinutes(2)) "saved ${savedClock(at, now, zone)}" else null
    }
}

private fun savedClock(at: Instant, now: Instant, zone: ZoneId): String {
    val date = at.atZone(zone).toLocalDate()
    val day = if (date == now.atZone(zone).toLocalDate()) "" else "${date.dayOfMonth} ${date.month.getDisplayName(java.time.format.TextStyle.SHORT, Locale.UK)} "
    return day + BudgetText.clock(at, zone)
}

/** Equal square segments in one 1px box, the one in force filled (the panel's .segments). */
@Composable
internal fun <T> Segments(items: List<T>, current: T, label: (T) -> String, onSelect: (T) -> Unit) {
    val colors = panelColors()
    Row(Modifier.fillMaxWidth().height(44.dp).border(1.dp, colors.fg)) {
        items.forEachIndexed { index, item ->
            val active = item == current
            Box(
                Modifier.weight(1f).fillMaxHeight().background(if (active) colors.fg else colors.panel)
                    .drawBehind { if (index > 0) drawRect(colors.fg, Offset.Zero, Size(1.dp.toPx(), size.height)) }
                    .semantics { selected = active }
                    .clickable(role = Role.Tab) { onSelect(item) },
                contentAlignment = Alignment.Center,
            ) {
                Text(label(item), style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, color = if (active) colors.bg else colors.fg, fontWeight = if (active) FontWeight.Bold else null))
            }
        }
    }
}

/** "Fri 27 Nov", "Today" for today. */
fun dayLabel(day: String, today: String): String {
    if (day == today) return "Today"
    val date = runCatching { LocalDate.parse(day) }.getOrNull() ?: return day
    return DateTimeFormatter.ofPattern("EEE d MMM", Locale.UK).format(date)
}

/** The day switcher: ‹ day ›, and Today when another day is shown. */
@Composable
private fun DaySwitcher(day: String, today: String, onMove: (Long) -> Unit, onToday: () -> Unit) {
    val colors = panelColors()
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.width(44.dp).height(44.dp).border(1.dp, colors.fg).clickable(role = Role.Button) { onMove(-1) }.semantics { contentDescription = "The day before" },
            contentAlignment = Alignment.Center) { PText("‹", size = 18.sp) }
        PText(dayLabel(day, today), Modifier.weight(1f).padding(horizontal = 12.dp), size = 15.sp, weight = FontWeight.Bold)
        if (day != today) ActWord("Today", onClick = onToday)
        val canForward = day < today
        Box(Modifier.width(44.dp).height(44.dp).border(1.dp, if (canForward) colors.fg else colors.line)
            .clickable(enabled = canForward, role = Role.Button) { onMove(1) }.semantics { contentDescription = "The day after" },
            contentAlignment = Alignment.Center) { PText("›", size = 18.sp, color = if (canForward) colors.fg else colors.line) }
    }
}

/**
 * A sideways swipe of at least 64dp, reported once the finger lifts; a
 * vertical drag stays the page's scroll. Gestures that start where
 * [inside] says (the map, which pans itself) are left alone.
 */
internal fun Modifier.swipeDays(inside: (Offset) -> Boolean = { false }, onSwipe: (dx: Float, threshold: Float) -> Unit): Modifier = pointerInput(Unit) {
    val threshold = 64.dp.toPx()
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false)
        if (inside(down.position)) return@awaitEachGesture
        val start = down.position.x
        var end = start
        val drag = awaitHorizontalTouchSlopOrCancellation(down.id) { change, _ ->
            change.consume()
            end = change.position.x
        } ?: return@awaitEachGesture
        end = drag.position.x
        val lifted = horizontalDrag(drag.id) { change ->
            change.consume()
            end = change.position.x
        }
        if (lifted) onSwipe(end - start, threshold)
    }
}

@Composable
fun ActivityScreen(data: ActivityData, now: Instant, zone: ZoneId) {
    val section = data.section
    val today = data.today
    // Where the map is, so a swipe that starts on it pans the map, not the day.
    val map = remember { arrayOfNulls<Rect>(1) }
    val page = remember { arrayOf(Offset.Zero) }
    AppPage(
        Modifier.onGloballyPositioned { page[0] = it.positionInWindow() }
            .swipeDays(inside = { at -> map[0]?.contains(page[0] + at) == true }, onSwipe = data::swipe),
    ) {
        PageHead("Activity")
        Segments(ActivitySection.entries, section, { it.label }, data::select)
        DaySwitcher(data.day, today, data::move, data::goToday)
        Gap(10.dp)
        when (section) {
            ActivitySection.TIMELINE -> TimelineView(data.timeline(), now, zone)
            ActivitySection.APPS -> AppsView(data.summary(), now, zone)
            ActivitySection.HEALTH -> HealthView(data.health(), now, zone, data.day, today)
            ActivitySection.PLACES -> PlacesView(data.places(), now, zone) { map[0] = it }
        }
    }
}

@Composable
internal fun <T> StaleNote(shown: Shown<T>, now: Instant, zone: ZoneId) {
    staleLine(shown, now, zone)?.let {
        Small(it)
        Gap(8.dp)
    }
}

/**
 * An answer as [LoadState] says to draw it: the answer with its muted line;
 * grey shapes in the real layout while it comes, also after a switch of
 * day or period; [unknown] once the try failed with nothing saved.
 */
@Composable
internal fun <T : Any> Loaded(shown: Shown<T>, now: Instant, zone: ZoneId, unknown: String, placeholder: @Composable () -> Unit, content: @Composable (T) -> Unit) {
    when (val showing = LoadState.of(shown)) {
        Showing.Placeholder -> placeholder()
        is Showing.Ready -> {
            StaleNote(shown, now, zone)
            content(showing.value)
        }
        Showing.Failed -> {
            StaleNote(shown, now, zone)
            Small(unknown)
        }
    }
}

internal const val NEVER_SAVED = "Unknown: this day was never saved on this phone."

@Composable
private fun TimelineView(shown: Shown<DayView>, now: Instant, zone: ZoneId) {
    Loaded(shown, now, zone, NEVER_SAVED, placeholder = {
        PanelCard("Distractions") { PlaceholderLines(1) }
        PanelCard("") { PlaceholderLines(6) }
    }) { view -> Timeline(view, zone) }
}

@Composable
private fun Timeline(view: DayView, zone: ZoneId) {
    val colors = panelColors()
    val devices = DayModel.timeline(view)
    PanelCard("Distractions", ActivityText.minutes(view.distractionMinutes)) {
        val parts = devices.filter { it.distractionMinutes >= 1 }.map { "${ActivityText.deviceName(it.device)} ${ActivityText.minutes(it.distractionMinutes)}" }
        Small(if (parts.isEmpty()) "No time on distractions this day." else parts.joinToString(" · "), Modifier.padding(top = 6.dp))
    }
    if (devices.isEmpty()) {
        Small("Nothing recorded this day.")
        return
    }
    for (device in devices) {
        val note = buildString {
            append("${ActivityText.minutes(device.activeMinutes)} active")
            if (device.noDataMinutes >= 15) append(" · no data for ${ActivityText.minutes(device.noDataMinutes)}")
        }
        PanelCard(ActivityText.deviceName(device.device), note) {
            device.rows.forEachIndexed { index, row ->
                val last = index == device.rows.lastIndex
                Row(
                    Modifier.fillMaxWidth().drawBehind {
                        if (!last) drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
                    }.padding(vertical = 6.dp),
                ) {
                    PText(DayModel.span(row, zone), Modifier.width(104.dp), size = 12.5.sp, color = if (row is TimelineRow.NoData) colors.muted else colors.fg)
                    when (row) {
                        is TimelineRow.NoData -> Small("no data · ${ActivityText.minutes((row.end.toEpochMilli() - row.start.toEpochMilli()) / 60_000.0)}", Modifier.weight(1f))
                        is TimelineRow.Use -> Column(Modifier.weight(1f)) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                PText(ActivityText.minutes(row.minutes), size = 12.5.sp)
                                if (row.distraction) Tag("distraction")
                            }
                            val line = DayModel.itemsLine(row.items)
                            if (line.isNotEmpty()) Small(line)
                        }
                    }
                }
            }
        }
    }
    Small("Gaps marked no data mean the device sent nothing then: unknown, not idle.")
}

@Composable
private fun Tag(text: String) {
    val colors = panelColors()
    Text(text.uppercase(), Modifier.padding(start = 6.dp).border(1.dp, colors.line).padding(horizontal = 4.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 10.sp, color = colors.fg))
}

@Composable
private fun AppsView(shown: Shown<ActivitySummary>, now: Instant, zone: ZoneId) {
    Loaded(shown, now, zone, NEVER_SAVED, placeholder = {
        PanelCard("") { PlaceholderLines(4) }
        PanelCard("") { PlaceholderLines(6) }
    }) { summary -> Apps(summary) }
}

@Composable
private fun Apps(summary: ActivitySummary) {
    val colors = panelColors()
    if (summary.insights.isNotEmpty()) {
        PanelCard("Where the time went") {
            summary.insights.forEach { insight ->
                Column(Modifier.fillMaxWidth().padding(top = 6.dp)) {
                    PText(insight.text, size = 14.sp, weight = FontWeight.Bold)
                    if (insight.detail.isNotBlank()) Small(insight.detail)
                }
            }
        }
    }
    for (device in summary.devices) {
        PanelCard(ActivityText.deviceName(device.device), cyou.rime.telemetry.collector.SummaryModel.deviceNote(device)) {
            val labels = device.labels.filter { it.minutes >= 1 }
            if (labels.isNotEmpty()) {
                Gap(6.dp)
                for (label in labels) {
                    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                        PText(label.name, Modifier.width(112.dp), size = 12.5.sp, color = if (label.name == "unlabeled") colors.muted else colors.fg, maxLines = 1)
                        Box(Modifier.weight(1f).height(8.dp)) {
                            Box(Modifier.fillMaxWidth((label.minutes / device.activeMinutes.coerceAtLeast(1.0)).toFloat().coerceIn(0.02f, 1f)).height(8.dp).background(colors.fg.copy(alpha = 0.55f)))
                        }
                        PText(ActivityText.minutes(label.minutes), Modifier.padding(start = 8.dp), size = 12.5.sp)
                    }
                }
            }
            UsageList("Apps", device.apps, "Nothing recorded.")
            if (device.sites.isNotEmpty()) UsageList("Sites", device.sites, "")
        }
    }
    Small("Labels are kept in Settings, under What counts as a distraction.")
}

@Composable
private fun UsageList(title: String, items: List<cyou.rime.telemetry.collector.Usage>, empty: String) {
    val colors = panelColors()
    Small(title, Modifier.padding(top = 10.dp, bottom = 2.dp))
    if (items.isEmpty() && empty.isNotEmpty()) Small(empty)
    items.forEach { item ->
        Row(Modifier.fillMaxWidth().drawBehind {
            drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
        }.padding(vertical = 5.dp), verticalAlignment = Alignment.CenterVertically) {
            PText(item.name, Modifier.weight(1f, fill = false), size = 13.5.sp, maxLines = 1)
            if (item.label.isNotBlank() && item.label != "unlabeled") Tag(item.label)
            Box(Modifier.weight(1f, fill = true))
            PText(ActivityText.minutes(item.minutes), size = 13.5.sp)
        }
    }
}

@Composable
private fun HealthView(shown: Shown<HealthDayView>, now: Instant, zone: ZoneId, day: String, today: String) {
    Loaded(shown, now, zone, "Unknown: nothing was read from Health Connect yet.", placeholder = {
        PanelCard(dayLabel(day, today)) {
            Gap(4.dp)
            for (label in listOf("Sleep", "Heart rate", "Steps")) {
                Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                    Small(label, Modifier.width(96.dp))
                    PlaceholderBar(0.5f)
                }
            }
        }
    }) { view -> Health(view, today) }
}

@Composable
private fun Health(view: HealthDayView, today: String) {
    val colors = panelColors()
    if (view.missing.isNotEmpty()) Small("Health Connect doesn't let the app read ${view.missing.joinToString(", ")}. Allow it in Settings › Permissions.", Modifier.padding(bottom = 8.dp))
    val day = view.day
    PanelCard(dayLabel(day.date, today)) {
        Gap(4.dp)
        HealthLine("Sleep", HealthDays.sleepText(day.sleepMinutes))
        HealthLine("Heart rate", HealthDays.heartText(day))
        HealthLine("Steps", HealthDays.stepsText(day.steps))
    }
    // The day's steps hour by hour.
    val hours = view.stepsByHour
    if (hours.any { (it ?: 0L) > 0L }) PanelCard("Steps") {
        val most = hours.maxOf { it ?: 0L }.coerceAtLeast(1L)
        Canvas(Modifier.fillMaxWidth().height(80.dp).padding(top = 8.dp).semantics { contentDescription = "Steps per hour" }) {
            val gap = 2.dp.toPx()
            val width = (size.width - gap * (hours.size - 1)) / hours.size
            hours.forEachIndexed { index, steps ->
                val x = index * (width + gap)
                if (steps == null || steps == 0L) drawRect(colors.line, Offset(x, size.height - 1.dp.toPx()), Size(width, 1.dp.toPx()))
                else {
                    val height = (steps.toFloat() / most) * size.height
                    drawRect(colors.fg, Offset(x, size.height - height), Size(width, height.coerceAtLeast(1f)))
                }
            }
        }
        ClockAxis()
    }
    // Heart rate through the day, broken where nothing was recorded for half an hour.
    val heart = view.heart
    if (heart.size >= 2) PanelCard("Heart rate") {
        val low = heart.minOf { it.bpm }
        val high = heart.maxOf { it.bpm }.coerceAtLeast(low + 10)
        Canvas(Modifier.fillMaxWidth().height(80.dp).padding(top = 8.dp).semantics { contentDescription = "Heart rate through the day" }) {
            fun x(minute: Int) = minute / 1440f * size.width
            fun y(bpm: Long) = size.height - (bpm - low).toFloat() / (high - low) * size.height
            drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
            val runs = mutableListOf<MutableList<cyou.rime.telemetry.collector.HeartPoint>>()
            for (point in heart) {
                val run = runs.lastOrNull()
                if (run != null && point.minute - run.last().minute <= 30) run += point else runs += mutableListOf(point)
            }
            for (run in runs) {
                if (run.size == 1) {
                    val point = run[0]
                    drawRect(colors.fg, Offset(x(point.minute) - 1.dp.toPx(), y(point.bpm) - 1.dp.toPx()), Size(2.dp.toPx(), 2.dp.toPx()))
                    continue
                }
                val path = Path()
                run.forEachIndexed { index, point -> if (index == 0) path.moveTo(x(point.minute), y(point.bpm)) else path.lineTo(x(point.minute), y(point.bpm)) }
                drawPath(path, colors.fg, style = Stroke(1.5.dp.toPx()))
            }
        }
        ClockAxis()
    }
}

/** 00:00, 12:00 and 24:00 under a chart of the day. */
@Composable
private fun ClockAxis() {
    Box(Modifier.fillMaxWidth()) {
        Small("00:00", Modifier.align(Alignment.CenterStart))
        Small("12:00", Modifier.align(Alignment.Center))
        Small("24:00", Modifier.align(Alignment.CenterEnd))
    }
}

@Composable
private fun HealthLine(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
        Small(label, Modifier.width(96.dp))
        PText(value, Modifier.weight(1f), size = 14.sp)
    }
}

@Composable
private fun PlacesView(shown: Shown<PlacesDay>, now: Instant, zone: ZoneId, onMap: (Rect?) -> Unit) {
    val colors = panelColors()
    DisposableEffect(Unit) { onDispose { onMap(null) } }
    Loaded(shown, now, zone, NEVER_SAVED, placeholder = {
        onMap(null)
        // The map's frame, empty, and grey lines where the day's places go.
        Box(Modifier.fillMaxWidth().height(360.dp).border(1.dp, colors.fg).padding(1.dp).background(colors.soft))
        Gap(10.dp)
        PlaceholderLines(3)
    }) { day ->
        if (day.positions.isEmpty() && day.stays.isEmpty()) {
            onMap(null)
            Small("No positions this day.")
        } else {
            PlacesMap(day, Modifier.fillMaxWidth().height(360.dp).border(1.dp, colors.fg).padding(1.dp).clipToBounds()
                .onGloballyPositioned { onMap(it.boundsInWindow()) })
            Gap(10.dp)
            Places.list(day).forEach { item ->
                PText(item.text, Modifier.fillMaxWidth().drawBehind {
                    drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
                }.padding(vertical = 5.dp), size = 13.5.sp, color = if (item.kind == PlaceItem.Kind.GAP) colors.muted else colors.fg)
            }
            Gap(6.dp)
            Small("From the phone's GPS. Positions worse than 100 m are left out. Inside a building GPS is weak, so short steps may not show.")
        }
    }
}
