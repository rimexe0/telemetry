package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import cyou.rime.telemetry.collector.ActivityData
import cyou.rime.telemetry.collector.ActivitySection
import cyou.rime.telemetry.collector.ActivityText
import cyou.rime.telemetry.collector.Average
import cyou.rime.telemetry.collector.Cell
import cyou.rime.telemetry.collector.HealthDays
import cyou.rime.telemetry.collector.NearbyPlace
import cyou.rime.telemetry.collector.Period
import cyou.rime.telemetry.collector.PeriodActivity
import cyou.rime.telemetry.collector.PeriodHealth
import cyou.rime.telemetry.collector.PeriodModel
import cyou.rime.telemetry.collector.PeriodPlaces
import cyou.rime.telemetry.collector.PeriodText
import cyou.rime.telemetry.collector.PlaceRule
import cyou.rime.telemetry.collector.PlaceStay
import cyou.rime.telemetry.collector.PlacesDay
import cyou.rime.telemetry.collector.Periods
import cyou.rime.telemetry.collector.Scope
import cyou.rime.telemetry.collector.Usage
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

// Activity's week, month and year: each view's numbers as averages per day
// over the days that have data, with each day of the period drawn next to
// the others.

@Composable
internal fun PeriodView(data: ActivityData, now: Instant, zone: ZoneId, onMap: (Rect?) -> Unit) {
    val today = LocalDate.now(zone)
    val period = data.period
    when (data.section) {
        ActivitySection.TIMELINE -> Loaded(data.periods.activity(period, today), now, zone, NEVER_SAVED, placeholder = {
            PanelCard("Distractions") { PlaceholderLines(2) }
            PanelCard("") { PlaceholderLines(6) }
        }) { PeriodTimeline(it, period) }
        ActivitySection.APPS -> Loaded(data.periods.activity(period, today), now, zone, NEVER_SAVED, placeholder = {
            PanelCard("Distractions") { PlaceholderLines(3) }
            PanelCard("") { PlaceholderLines(6) }
        }) { PeriodApps(it, period) }
        ActivitySection.HEALTH -> Loaded(data.periods.health(period, today), now, zone, "Unknown: nothing was read from Health Connect yet.", placeholder = {
            PanelCard(Periods.label(period, today)) {
                Gap(4.dp)
                for (label in listOf("Sleep", "Heart rate", "Steps")) {
                    Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                        Small(label, Modifier.width(96.dp))
                        PlaceholderBar(0.5f)
                    }
                }
            }
        }) { PeriodHealthView(it, period, today) }
        ActivitySection.PLACES -> PeriodPlacesView(data, period, today, now, zone, onMap)
    }
}

private val shortDay = DateTimeFormatter.ofPattern("EEE d", Locale.UK)
private val dayMonth = DateTimeFormatter.ofPattern("d MMM", Locale.UK)
private val monthName = DateTimeFormatter.ofPattern("MMM", Locale.UK)

/** "7 h 20 min a day · 5 days", or "no data". */
private fun perDayLine(average: Average?, value: (Double) -> String): String =
    if (average == null) "no data" else "${value(average.perDay)} · ${PeriodText.over(average.days)}"

@Composable
private fun PeriodTimeline(view: PeriodActivity, period: Period) {
    PanelCard("Distractions", PeriodText.perDay(view.distraction)) {
        Small(view.distraction?.let { PeriodText.over(it.days) } ?: "No day with data in this ${period.scope.unit}.", Modifier.padding(top = 6.dp))
        for (device in view.devices) {
            val active = device.active ?: continue
            Small("${ActivityText.deviceName(device.device)} ${ActivityText.minutes(active.perDay)} active a day · ${PeriodText.over(active.days)}")
        }
    }
    PanelCard("") {
        Gap(6.dp)
        if (period.scope == Scope.YEAR) {
            // A month to a block, each day a thin line, so the year fits.
            view.days.groupBy { it.month }.forEach { (month, days) ->
                Row(Modifier.fillMaxWidth().padding(vertical = 3.dp)) {
                    Small(monthName.format(days.first()), Modifier.width(48.dp))
                    Box(Modifier.weight(1f)) {
                        DayBars(days.map { day -> view.rows[view.days.indexOf(day)]?.let(PeriodModel::cells) }, 3.dp)
                    }
                }
            }
        } else {
            view.days.forEachIndexed { index, day ->
                Row(Modifier.fillMaxWidth().padding(vertical = if (period.scope == Scope.WEEK) 4.dp else 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    Small(shortDay.format(day), Modifier.width(56.dp))
                    Box(Modifier.weight(1f)) { DayBars(listOf(view.rows[index]?.let(PeriodModel::cells)), if (period.scope == Scope.WEEK) 14.dp else 9.dp) }
                }
            }
        }
        Row(Modifier.fillMaxWidth()) {
            Box(Modifier.width(if (period.scope == Scope.YEAR) 48.dp else 56.dp))
            Box(Modifier.weight(1f)) { ClockAxis() }
        }
        Legend()
    }
    Small("Parts marked no data mean no device sent anything then: unknown, not idle.")
}

/**
 * Days from 00:00 to 24:00, one under the other, [height] each: distraction
 * solid, other use lighter, no data hatched; a day never kept is hatched
 * whole, never drawn as empty.
 */
@Composable
private fun DayBars(days: List<List<Cell>?>, height: Dp) {
    val colors = panelColors()
    Canvas(Modifier.fillMaxWidth().height(height * days.size)) {
        val rowHeight = size.height / days.size.coerceAtLeast(1)
        drawRect(colors.soft.copy(alpha = 0.5f), Offset.Zero, size)
        days.forEachIndexed { row, cells ->
            val parts = cells ?: List(144) { Cell.NO_DATA }
            if (parts.isEmpty()) return@forEachIndexed
            val top = row * rowHeight
            val width = size.width / parts.size
            var start = 0
            while (start < parts.size) {
                var end = start
                while (end + 1 < parts.size && parts[end + 1] == parts[start]) end++
                val x = start * width
                val w = (end - start + 1) * width
                when (parts[start]) {
                    Cell.DISTRACTION -> drawRect(colors.fg, Offset(x, top), Size(w, rowHeight))
                    Cell.ACTIVE -> drawRect(colors.fg.copy(alpha = 0.4f), Offset(x, top), Size(w, rowHeight))
                    Cell.NO_DATA -> clipRect(x, top, x + w, top + rowHeight) { hatch(x, w, colors.line) }
                    Cell.LATER -> drawRect(colors.bg, Offset(x, top), Size(w, rowHeight))
                    Cell.IDLE -> Unit
                }
                start = end + 1
            }
        }
    }
}

private fun DrawScope.hatch(x: Float, w: Float, color: androidx.compose.ui.graphics.Color) = clipRect(x, 0f, x + w, size.height) {
    val step = 4.dp.toPx()
    var at = x - size.height
    while (at < x + w) {
        drawLine(color, Offset(at, size.height), Offset(at + size.height, 0f), 1.dp.toPx())
        at += step
    }
}

@Composable
private fun Legend() {
    val colors = panelColors()
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        for ((cell, name) in listOf(Cell.DISTRACTION to "distraction", Cell.ACTIVE to "active", Cell.NO_DATA to "no data")) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(10.dp).border(1.dp, colors.line)) { DayBars(listOf(listOf(cell)), 10.dp) }
                Small(name, Modifier.padding(start = 4.dp))
            }
        }
    }
}

/**
 * A bar for each day of the period, its height [values] over the most; a
 * day without data a hatched stub, never a zero bar. Under it the period's
 * first and last day, or the week's days.
 */
@Composable
private fun DayChart(values: List<Double?>, period: Period, description: String, floor: Double = 0.0) {
    val colors = panelColors()
    val most = (values.filterNotNull().maxOrNull() ?: 0.0).coerceAtLeast(floor + 1.0)
    // The whole period has a place, days to come stay empty.
    val count = Periods.days(period, period.end).size
    Canvas(Modifier.fillMaxWidth().height(80.dp).padding(top = 8.dp).semantics { contentDescription = description }) {
        val gap = if (count <= 31) 2.dp.toPx() else 0f
        val width = (size.width - gap * (count - 1)) / count
        drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
        values.forEachIndexed { index, value ->
            val x = index * (width + gap)
            if (value == null) {
                clipRect(x, size.height - 6.dp.toPx(), x + width.coerceAtLeast(1f), size.height) { hatch(x, width.coerceAtLeast(1f), colors.line) }
            } else if (value > floor) {
                val height = ((value - floor) / (most - floor)).toFloat() * size.height
                drawRect(colors.fg, Offset(x, size.height - height), Size(width.coerceAtLeast(1f), height.coerceAtLeast(1f)))
            }
        }
    }
    if (period.scope == Scope.WEEK) {
        Row(Modifier.fillMaxWidth()) {
            Periods.days(period, period.end).forEach { Small(it.dayOfWeek.getDisplayName(java.time.format.TextStyle.SHORT, Locale.UK), Modifier.weight(1f)) }
        }
    } else {
        Box(Modifier.fillMaxWidth()) {
            val format = if (period.scope == Scope.YEAR) monthName else dayMonth
            Small(format.format(period.start), Modifier.align(Alignment.CenterStart))
            Small(format.format(period.end), Modifier.align(Alignment.CenterEnd))
        }
    }
}

@Composable
private fun PeriodApps(view: PeriodActivity, period: Period) {
    PanelCard("Distractions", PeriodText.perDay(view.distraction)) {
        DayChart(view.rows.map { row -> row?.takeIf { it.reported }?.distractionMinutes }, period, "Distraction minutes each day")
        Small(view.distraction?.let { PeriodText.over(it.days) } ?: "No day with data in this ${period.scope.unit}.", Modifier.padding(top = 4.dp))
    }
    view.agents?.let { agents ->
        PanelCard("Agents", PeriodText.perDay(agents)) {
            Small("Time coding agents worked for you · ${PeriodText.over(agents.days)}", Modifier.padding(top = 6.dp))
        }
    }
    for (device in view.devices) {
        val active = device.active
        PanelCard(ActivityText.deviceName(device.device), if (active == null) "" else "${PeriodText.perDay(active)} · ${PeriodText.over(active.days)}") {
            UsageList("Apps, a day", device.apps.map { Usage(it.name, it.label, it.perDay) }, "Nothing recorded.")
            if (device.sites.isNotEmpty()) UsageList("Sites, a day", device.sites.map { Usage(it.name, it.label, it.perDay) }, "")
        }
    }
    Small("Labels are kept in Settings, under What counts as a distraction.")
}

@Composable
private fun PeriodHealthView(view: PeriodHealth, period: Period, today: LocalDate) {
    if (view.missing.isNotEmpty()) Small("Health Connect doesn't let the app read ${view.missing.joinToString(", ")}. Allow it in Settings › Permissions.", Modifier.padding(bottom = 8.dp))
    PanelCard(Periods.label(period, today)) {
        Gap(4.dp)
        HealthLine("Sleep", perDayLine(view.sleep) { "${ActivityText.minutes(it)} a night" })
        HealthLine("Heart rate", perDayLine(view.heart) { "${Math.round(it)} bpm average" })
        HealthLine("Steps", perDayLine(view.steps) { "${HealthDays.stepsText(Math.round(it))} a day" })
    }
    if (view.sleep != null) PanelCard("Sleep") { DayChart(view.rows.map { it?.sleepMinutes?.toDouble() }, period, "Sleep each night") }
    if (view.heart != null) PanelCard("Heart rate") {
        val known = view.rows.mapNotNull { it?.heartAvg?.toDouble() }
        DayChart(view.rows.map { it?.heartAvg?.toDouble() }, period, "Average heart rate each day", floor = (known.minOrNull() ?: 0.0) - 10)
    }
    if (view.steps != null) PanelCard("Steps") { DayChart(view.rows.map { it?.steps?.toDouble() }, period, "Steps each day") }
}

@Composable
private fun PeriodPlacesView(data: ActivityData, period: Period, today: LocalDate, now: Instant, zone: ZoneId, onMap: (Rect?) -> Unit) {
    val colors = panelColors()
    DisposableEffect(Unit) { onDispose { onMap(null) } }
    Loaded(data.periods.places(period, today), now, zone, NEVER_SAVED, placeholder = {
        onMap(null)
        Box(Modifier.fillMaxWidth().height(360.dp).border(1.dp, colors.fg).padding(1.dp).background(colors.soft))
        Gap(10.dp)
        PlaceholderLines(3)
    }) { view ->
        val rows = data.periods.rowsOf(period, today)
        val rules = data.periods.rules
        // A week shows its positions as dots; a month or a year only where the phone stayed.
        val positions = if (period.scope == Scope.WEEK) Periods.days(period, today).flatMap { data.places(it.toString()).value?.positions.orEmpty() } else emptyList()
        val stays = rows.flatMap { row ->
            row.stays.map { stay ->
                val name = rules.firstOrNull { PlaceRule.meters(stay.lat, stay.lon, it.lat, it.lon) <= it.radiusM }?.name ?: stay.name
                PlaceStay(stay.from, stay.to, stay.lat, stay.lon, stay.home, if (name.isEmpty()) emptyList() else listOf(NearbyPlace(name, stay.lat, stay.lon, 0.0)))
            }
        }
        if (positions.isEmpty() && stays.isEmpty()) {
            onMap(null)
            Small("No positions this ${period.scope.unit}.")
        } else {
            val from = period.start.atStartOfDay(zone).toInstant()
            val to = minOf(period.end.plusDays(1).atStartOfDay(zone).toInstant(), now)
            PlacesMap(PlacesDay(period.key, zone, from, to, positions, stays, rules),
                Modifier.fillMaxWidth().height(360.dp).border(1.dp, colors.fg).padding(1.dp).clipToBounds().onGloballyPositioned { onMap(it.boundsInWindow()) },
                period = true)
        }
        Gap(10.dp)
        PeriodPlacesList(view)
        Gap(6.dp)
        Small("From the phone's GPS. Positions worse than 100 m are left out. Inside a building GPS is weak, so short steps may not show.")
    }
}

@Composable
private fun PeriodPlacesList(view: PeriodPlaces) {
    val colors = panelColors()
    @Composable
    fun line(text: String, muted: Boolean = false) = PText(text, Modifier.fillMaxWidth().drawBehind {
        drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
    }.padding(vertical = 5.dp), size = 13.5.sp, color = if (muted) colors.muted else colors.fg)
    line("Away from home ${perDayLine(view.away) { "${ActivityText.minutes(it)} a day" }}", muted = view.away == null)
    line("Moved ${perDayLine(view.distanceKm) { "%.1f km a day".format(Locale.UK, it) }}", muted = view.distanceKm == null)
    if (view.places.isNotEmpty()) {
        Small("Stayed at most", Modifier.padding(top = 10.dp, bottom = 2.dp))
        view.places.forEach { line(PeriodModel.placeLine(it)) }
    }
}
