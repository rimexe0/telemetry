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
import androidx.compose.runtime.Composable
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
import cyou.rime.telemetry.collector.HealthWeek
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
internal fun AppPage(content: @Composable () -> Unit) {
    val colors = panelColors()
    Column(Modifier.fillMaxSize().background(colors.bg).verticalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, top = 16.dp, bottom = 24.dp)) {
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
 * The muted line over a saved answer: nothing while it is fresh; "saved
 * 14:20" while a fresh one is being asked for; the server's own words when
 * it refused; Today's words when it can't be reached.
 */
fun <T> staleLine(shown: Shown<T>, now: Instant, zone: ZoneId): String? {
    val at = shown.at
    return when (val refresh = shown.refresh) {
        Refresh.Unreachable -> TodayText.saved(at, now, zone)
        is Refresh.Refused -> refresh.message + (at?.let { " · saved ${BudgetText.clock(it, zone)}" } ?: "")
        Refresh.Fresh -> null
        Refresh.Never -> when {
            at != null && Duration.between(at, now) >= Duration.ofMinutes(2) -> "saved ${savedClock(at, now, zone)}"
            at == null && shown.value == null && shown.loading -> "Loading…"
            else -> null
        }
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

@Composable
fun ActivityScreen(data: ActivityData, now: Instant, zone: ZoneId) {
    val section = data.section
    val today = data.today
    if (section == ActivitySection.PLACES) {
        // The map takes the page's height; the list scrolls under it.
        AppPage {
            PageHead("Activity")
            Segments(ActivitySection.entries, section, { it.label }, data::select)
            DaySwitcher(data.day, today, data::move, data::goToday)
            Gap(10.dp)
            PlacesView(data.places(), now, zone)
        }
        return
    }
    AppPage {
        PageHead("Activity")
        Segments(ActivitySection.entries, section, { it.label }, data::select)
        if (section != ActivitySection.HEALTH) DaySwitcher(data.day, today, data::move, data::goToday)
        else Small("The last two weeks", Modifier.padding(top = 10.dp))
        Gap(10.dp)
        when (section) {
            ActivitySection.TIMELINE -> TimelineView(data.timeline(), now, zone)
            ActivitySection.APPS -> AppsView(data.summary(), now, zone)
            ActivitySection.HEALTH -> HealthView(data.health, now, zone)
            ActivitySection.PLACES -> Unit
        }
    }
}

@Composable
private fun <T> StaleNote(shown: Shown<T>, now: Instant, zone: ZoneId) {
    staleLine(shown, now, zone)?.let {
        Small(it)
        Gap(8.dp)
    }
}

@Composable
private fun TimelineView(shown: Shown<DayView>, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    StaleNote(shown, now, zone)
    val view = shown.value ?: run {
        if (shown.refresh != Refresh.Never || !shown.loading) Small("Unknown: this day was never saved on this phone.")
        return
    }
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
    val colors = panelColors()
    StaleNote(shown, now, zone)
    val summary = shown.value ?: run {
        if (shown.refresh != Refresh.Never || !shown.loading) Small("Unknown: this day was never saved on this phone.")
        return
    }
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
private fun HealthView(shown: Shown<HealthWeek>, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    if (shown.refresh is Refresh.Refused) Small((shown.refresh as Refresh.Refused).message, Modifier.padding(bottom = 8.dp))
    val week = shown.value ?: run {
        if (!shown.loading) Small("Unknown: nothing was read from Health Connect yet.")
        else Small("Loading…")
        return
    }
    if (week.missing.isNotEmpty()) Small("Health Connect doesn't let the app read ${week.missing.joinToString(", ")}. Allow it in Settings › Permissions.", Modifier.padding(bottom = 8.dp))
    val today = week.days.firstOrNull()
    if (today != null) {
        PanelCard("Today", dayLabel(today.date, LocalDate.now(zone).toString()).takeIf { it != "Today" }.orEmpty()) {
            Gap(4.dp)
            HealthLine("Sleep", HealthDays.sleepText(today.sleepMinutes))
            HealthLine("Heart rate", HealthDays.heartText(today))
            HealthLine("Steps", HealthDays.stepsText(today.steps))
        }
    }
    // Steps per day as bars, newest on the right, and sleep under them.
    val days = week.days.reversed()
    PanelCard("Steps") {
        val most = days.maxOfOrNull { it.steps ?: 0L }?.coerceAtLeast(1L) ?: 1L
        Canvas(Modifier.fillMaxWidth().height(80.dp).padding(top = 8.dp).semantics { contentDescription = "Steps per day" }) {
            val gap = 3.dp.toPx()
            val width = (size.width - gap * (days.size - 1)) / days.size
            days.forEachIndexed { index, day ->
                val x = index * (width + gap)
                val steps = day.steps
                if (steps == null) {
                    drawRect(colors.line, Offset(x, size.height - 1.dp.toPx()), Size(width, 1.dp.toPx()))
                } else {
                    val height = (steps.toFloat() / most) * size.height
                    drawRect(colors.fg, Offset(x, size.height - height), Size(width, height.coerceAtLeast(1f)))
                }
            }
        }
        Row(Modifier.fillMaxWidth()) {
            Small(days.firstOrNull()?.date?.let { dayLabel(it, "") }.orEmpty(), Modifier.weight(1f))
            Small("today")
        }
    }
    PanelCard("Each day") {
        week.days.forEachIndexed { index, day ->
            val last = index == week.days.lastIndex
            Column(Modifier.fillMaxWidth().drawBehind {
                if (!last) drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
            }.padding(vertical = 6.dp)) {
                PText(dayLabel(day.date, LocalDate.now(zone).toString()), size = 13.5.sp, weight = FontWeight.Bold)
                Small("${HealthDays.sleepText(day.sleepMinutes)} sleep · ${HealthDays.stepsText(day.steps)}")
                Small(HealthDays.heartText(day))
            }
        }
    }
    shown.at?.let { Small("Read from Health Connect on this phone at ${BudgetText.clock(it, zone)}.") }
}

@Composable
private fun HealthLine(label: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
        Small(label, Modifier.width(96.dp))
        PText(value, Modifier.weight(1f), size = 14.sp)
    }
}

@Composable
private fun PlacesView(shown: Shown<PlacesDay>, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    StaleNote(shown, now, zone)
    val day = shown.value ?: run {
        if (!shown.loading) Small("Unknown: this day was never saved on this phone.")
        return
    }
    val empty = day.positions.isEmpty() && day.stays.isEmpty()
    if (empty) {
        Small("No positions this day.")
        return
    }
    PlacesMap(day, Modifier.fillMaxWidth().height(360.dp).border(1.dp, colors.fg).padding(1.dp).clipToBounds())
    Gap(10.dp)
    val items = Places.list(day)
    items.forEach { item ->
        PText(item.text, Modifier.fillMaxWidth().drawBehind {
            drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
        }.padding(vertical = 5.dp), size = 13.5.sp, color = if (item.kind == PlaceItem.Kind.GAP) colors.muted else colors.fg)
    }
    Gap(6.dp)
    Small("From the phone's GPS. Positions worse than 100 m are left out. Inside a building GPS is weak, so short steps may not show.")
}
