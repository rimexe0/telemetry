package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.zIndex
import cyou.rime.telemetry.collector.BudgetText
import cyou.rime.telemetry.collector.CellState
import cyou.rime.telemetry.collector.HabitCells
import cyou.rime.telemetry.collector.HabitsState
import cyou.rime.telemetry.collector.HeatmapRow
import cyou.rime.telemetry.collector.TickQueue
import cyou.rime.telemetry.collector.TodayState
import cyou.rime.telemetry.collector.TodayText
import cyou.rime.telemetry.collector.WeekCell
import kotlinx.coroutines.flow.first

/** The app's four places, as the panel's bottom bar names them. */
enum class Place(val label: String) { TODAY("Today"), HABITS("Habits"), ACTIVITY("Activity"), SETTINGS("Settings") }

/** The bar along the bottom: the place in use filled (web/src/lib/PlaceNav.svelte). */
@Composable
fun PlaceBar(current: Place, onSelect: (Place) -> Unit) {
    val colors = panelColors()
    Row(
        Modifier.fillMaxWidth().background(colors.bg).drawBehind {
            drawRect(colors.fg, Offset.Zero, Size(size.width, 2.dp.toPx()))
        }.padding(top = 2.dp),
    ) {
        for (place in Place.entries) {
            val active = place == current
            Box(
                Modifier.weight(1f).height(52.dp).background(if (active) colors.fg else colors.bg)
                    .semantics { selected = active }
                    .clickable(role = Role.Tab) { onSelect(place) },
                contentAlignment = Alignment.Center,
            ) {
                PText(place.label, color = if (active) colors.bg else colors.fg, size = 13.5.sp, weight = if (active) FontWeight.Bold else null)
            }
        }
    }
}

/** A page of the app: scrolls, with the panel's side gutter. */
@Composable
private fun Page(content: @Composable () -> Unit) {
    val colors = panelColors()
    Column(Modifier.fillMaxSize().background(colors.bg).verticalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, top = 16.dp, bottom = 24.dp)) {
        content()
    }
}

/** An underlined word that acts, as the panel's link buttons. */
@Composable
private fun LinkWord(text: String, enabled: Boolean = true, onClick: () -> Unit) {
    val colors = panelColors()
    Text(
        text,
        Modifier.heightIn(min = 44.dp).clickable(enabled = enabled, role = Role.Button, onClick = onClick).padding(horizontal = 4.dp, vertical = 12.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, color = colors.fg, textDecoration = TextDecoration.Underline),
    )
}

@Composable
fun TodayScreen(
    state: TodayState,
    dateLabel: String,
    /** "Saved at 14:20 · the server can't be reached" while the server can't be reached. */
    stale: String?,
    notice: String?,
    /** Why the door could not be cancelled, when it could not. */
    doorNotice: String?,
    zone: java.time.ZoneId,
    doorBusy: Boolean,
    onTick: (Long, Boolean) -> Unit,
    onCancelDoor: (Long) -> Unit,
) = Page {
    val colors = panelColors()
    PageHead("Today", dateLabel)
    if (stale != null) {
        Small(stale)
        Gap(10.dp)
    }

    // Habits first: they are the point of the day (owner 2026-11-26).
    val habits = state.habits
    PanelCard("Habits", if (!habits.isNullOrEmpty()) "${habits.count { it.done }} of ${habits.size}" else "") {
        when {
            habits == null -> Small("Unknown: today's habits were never saved on this phone.", Modifier.padding(top = 6.dp))
            habits.isEmpty() -> Small("No habits scheduled today.", Modifier.padding(top = 6.dp))
            else -> habits.forEachIndexed { index, habit ->
                val last = index == habits.lastIndex
                Column(
                    Modifier.fillMaxWidth().drawBehind {
                        if (!last) drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
                    }.padding(top = 2.dp, bottom = if (last) 2.dp else 10.dp),
                ) {
                    Row(
                        Modifier.tapRow("${habit.name}, ${if (habit.done) "done" else "not done"}", enabled = state.ticksOpen) { onTick(habit.id, !habit.done) },
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        SquareCheck(habit.done, state.ticksOpen)
                        PText(habit.name, Modifier.weight(1f), color = if (habit.done) colors.muted else colors.fg, size = 15.sp)
                        val worth = HabitCells.worth(habit.value)
                        if (worth.isNotEmpty() && state.budget?.enabled != false) Small(worth)
                    }
                    WeekStrip(state.strips[habit.id].orEmpty(), letters = last, modifier = Modifier.padding(start = 32.dp))
                }
            }
        }
        if (!state.ticksOpen && !habits.isNullOrEmpty()) Small("Bedtime has started, so today's habits are closed.", Modifier.padding(top = 6.dp))
        if (notice != null) Small(notice, Modifier.padding(top = 6.dp))
    }

    // The distraction time, kept small. There is no way to open early here:
    // the block screen asks when a distraction is opened.
    val budget = state.budget
    PanelCard("Distraction time", state.bedtimeLabel) {
        when {
            budget == null && state.budgetUnknown -> Small("Unknown while the server can't be reached.", Modifier.padding(top = 6.dp))
            budget == null -> Small("Unknown: the budget was never saved on this phone.", Modifier.padding(top = 6.dp))
            !budget.enabled -> Small("The budget is off. Distractions follow the other rules.", Modifier.padding(top = 6.dp))
            else -> {
                Row(Modifier.fillMaxWidth().padding(top = 6.dp, bottom = 8.dp), verticalAlignment = Alignment.Bottom) {
                    Text(BudgetText.minutes(budget.left), style = TextStyle(fontFamily = Mono, fontSize = 20.sp, fontWeight = FontWeight.Bold, color = colors.fg))
                }
                Small("left of ${BudgetText.minutes(budget.budget)} · ${TodayText.budgetLine(budget, zone)}", Modifier.padding(bottom = 8.dp))
                ThinBar(TodayText.leftShare(budget), "Distraction time left")
                val door = state.door
                val doorId = state.doorId
                if (door != null) {
                    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("DOOR ${door.first.uppercase()}", Modifier.border(1.dp, colors.line).padding(horizontal = 4.dp),
                            style = TextStyle(fontFamily = Mono, fontSize = 10.5.sp, color = colors.fg))
                        PText(" ${door.second}", Modifier.weight(1f), size = 12.5.sp)
                        if (door.first == "waiting" && doorId != null) LinkWord("Cancel", enabled = !doorBusy) { onCancelDoor(doorId) }
                    }
                }
                if (doorNotice != null) Small(doorNotice, Modifier.padding(top = 4.dp))
            }
        }
    }

    PanelCard("Right now", state.rightNow?.since?.takeIf { it.isNotEmpty() && !state.budgetUnknown }?.let { "since $it" }.orEmpty()) {
        val now = state.rightNow
        when {
            state.budgetUnknown -> Small("Unknown while the server can't be reached.", Modifier.padding(top = 6.dp))
            now == null -> Small("Unknown: nothing was read from the Mac.", Modifier.padding(top = 6.dp))
            else -> {
                PText(now.line, Modifier.padding(vertical = 6.dp), size = 16.5.sp)
                now.note?.let { note ->
                    Row(Modifier.padding(vertical = 4.dp)) {
                        Box(Modifier.width(3.dp).height(18.dp).background(colors.fg))
                        Small(note, Modifier.padding(start = 8.dp), color = colors.fg)
                    }
                }
            }
        }
    }
}

/** One habit's last seven days across the row, today outlined; the weekday letters under the last. */
@Composable
fun WeekStrip(cells: List<WeekCell>, letters: Boolean, modifier: Modifier = Modifier) {
    if (cells.isEmpty()) return
    val colors = panelColors()
    Column(modifier.fillMaxWidth()) {
        Canvas(Modifier.fillMaxWidth().height(22.dp).semantics { contentDescription = "Last 7 days" }) {
            val gap = 4.dp.toPx()
            val inset = 2.dp.toPx()
            val width = (size.width - gap * (cells.size - 1) - inset * 2) / cells.size
            val height = 18.dp.toPx()
            cells.forEachIndexed { index, cell ->
                val topLeft = Offset(inset + index * (width + gap), inset)
                habitCell(cell.state, colors, topLeft, Size(width, height))
                if (cell.today) {
                    val px = 1.dp.toPx()
                    drawRect(colors.fg, topLeft - Offset(1.5f * px, 1.5f * px), Size(width + 3 * px, height + 3 * px), style = Stroke(px))
                }
            }
        }
        if (letters) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 2.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                for (cell in cells) {
                    Text(cell.letter.toString(), Modifier.weight(1f), textAlign = TextAlign.Center,
                        style = TextStyle(fontFamily = Mono, fontSize = 9.5.sp, color = if (cell.today) colors.fg else colors.muted, fontWeight = if (cell.today) FontWeight.Bold else null))
                }
            }
        }
    }
}

@Composable
fun HabitsScreen(state: HabitsState, stale: String?, onEditHabits: () -> Unit) = Page {
    val colors = panelColors()
    PageHead("Habits")
    if (stale != null) {
        Small(stale)
        Gap(10.dp)
    }
    PanelCard("The last year") {
        val heatmap = state.heatmap
        when {
            heatmap == null -> Small("Unknown: the year was never saved on this phone.", Modifier.padding(top = 6.dp))
            heatmap.habits.isEmpty() -> Small("No habits yet. Once you add one, its days show here.", Modifier.padding(top = 6.dp))
            else -> YearGrid(state)
        }
    }
    PanelCard("Reasons") {
        if (state.reasons.isEmpty()) Small("No reasons yet.", Modifier.padding(top = 6.dp))
        for (reason in state.reasons) PText(reason, Modifier.padding(top = 6.dp), size = 15.sp)
    }
    // Editing stays in the panel.
    Row(
        Modifier.tapRow("Edit habits", onClick = onEditHabits).border(1.dp, colors.line).padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        PText("Edit habits", Modifier.weight(1f), size = 15.sp)
        PText("›", color = colors.muted)
    }
}

private val CELL = 11.dp
private val CELL_GAP = 2.dp
private val LABELS = 26.dp
private val weekdayLabels = listOf("Mon", "", "Wed", "", "Fri", "", "")

/** All habits first, then one band per habit, scrolled to the newest days; names and weekdays stay in view. */
@Composable
private fun YearGrid(state: HabitsState) {
    val colors = panelColors()
    val heatmap = state.heatmap ?: return
    val layout = HabitCells.layout(state.openDay, 371)
    val scroll = rememberScrollState()
    LaunchedEffect(layout.weeks.size) {
        val end = snapshotFlow { scroll.maxValue }.first { it in 1 until Int.MAX_VALUE }
        scroll.scrollTo(end)
    }
    val rows = heatmap.habits.sortedBy { if (it.archivedDay != null) 1 else 0 }
    BoxWithConstraints(Modifier.fillMaxWidth().padding(top = 8.dp).border(1.dp, colors.line)) {
        val viewport = maxWidth - 20.dp
        Column(Modifier.horizontalScroll(scroll).padding(horizontal = 10.dp, vertical = 8.dp)) {
            Band("All habits", "Share of the day's habits done", null, scroll, viewport)
            Box(Modifier.width(LABELS + (CELL + CELL_GAP) * layout.weeks.size).height(14.dp)) {
                for ((label, column) in layout.months) {
                    Small(label, Modifier.offset(x = LABELS + (CELL + CELL_GAP) * column), color = colors.muted)
                }
            }
            Grid(layout.weeks, scroll) { day ->
                when (val level = HabitCells.level(heatmap.combined[day])) {
                    -1 -> CellState.UNKNOWN to null
                    0 -> CellState.MISSED to null
                    4 -> CellState.DONE to null
                    else -> CellState.DONE to lerp(colors.panel, colors.done, listOf(0f, .35f, .6f, .8f)[level])
                }
            }
            for (row in rows) {
                Gap(14.dp)
                Band(row.name, HabitCells.runs(row.currentRun, row.longestRun), row, scroll, viewport)
                val queued = TickQueue.doneByDay(state.queued, row.id)
                Grid(layout.weeks, scroll) { day ->
                    (if (queued[day] == true) CellState.DONE else HabitCells.year(row.days[day], day, state.openDay)) to null
                }
            }
        }
    }
}

/** A band's name and note, kept in view while the grid scrolls under it. */
@Composable
private fun Band(name: String, note: String, row: HeatmapRow?, scroll: ScrollState, viewport: androidx.compose.ui.unit.Dp) {
    val colors = panelColors()
    Row(
        Modifier.offset { IntOffset(scroll.value, 0) }.width(viewport).padding(bottom = 3.dp),
        verticalAlignment = Alignment.Bottom,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        PText(name, size = 13.5.sp, weight = FontWeight.Bold, maxLines = 1)
        if (row?.archivedDay != null) Text("ARCHIVED", Modifier.border(1.dp, colors.line).padding(horizontal = 4.dp), style = TextStyle(fontFamily = Mono, fontSize = 10.sp, color = colors.fg))
        Small(note, Modifier.weight(1f, fill = false), color = if (row == null) colors.muted else colors.fg)
    }
}

/** Seven rows of days, weeks as columns, with the weekday names kept at the left. */
@Composable
private fun Grid(weeks: List<List<String?>>, scroll: ScrollState, cell: (String) -> Pair<CellState, androidx.compose.ui.graphics.Color?>) {
    val colors = panelColors()
    val height = (CELL + CELL_GAP) * 7
    Box {
        Canvas(Modifier.width(LABELS + (CELL + CELL_GAP) * weeks.size).height(height)) {
            val step = (CELL + CELL_GAP).toPx()
            val side = CELL.toPx()
            val left = LABELS.toPx()
            weeks.forEachIndexed { column, week ->
                week.forEachIndexed { row, day ->
                    if (day != null) {
                        val (state, fill) = cell(day)
                        habitCell(state, colors, Offset(left + column * step, row * step), Size(side, side), fill)
                    }
                }
            }
        }
        Column(Modifier.offset { IntOffset(scroll.value, 0) }.zIndex(1f).width(LABELS - 2.dp).background(colors.panel)) {
            for (label in weekdayLabels) {
                Box(Modifier.size(LABELS - 2.dp, CELL + CELL_GAP), contentAlignment = Alignment.CenterStart) {
                    Text(label, style = TextStyle(fontFamily = Mono, fontSize = 9.sp, color = colors.muted, lineHeight = 9.sp))
                }
            }
        }
    }
}

/** What the Activity and Settings places show while the panel can't open. */
@Composable
fun PanelUnavailable(opening: Boolean, refusedWith: Int?, onRetry: () -> Unit, onPhoneSettings: () -> Unit) = Page {
    if (opening) {
        Small("Opening…")
        return@Page
    }
    PText(if (refusedWith != null) "The server didn't open the panel (it answered $refusedWith)." else "The server can't be reached.", size = 15.sp)
    Gap(6.dp)
    Small("This place needs it. Today and Habits work without it, and blocking keeps working on this phone.")
    Row {
        LinkWord("Retry", onClick = onRetry)
        LinkWord("Phone settings", onClick = onPhoneSettings)
    }
}
