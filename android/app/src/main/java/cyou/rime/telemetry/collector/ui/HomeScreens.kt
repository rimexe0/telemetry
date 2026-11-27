package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import cyou.rime.telemetry.collector.BudgetText
import cyou.rime.telemetry.collector.CellState
import cyou.rime.telemetry.collector.HabitCells
import cyou.rime.telemetry.collector.HabitsState
import cyou.rime.telemetry.collector.HabitRows
import cyou.rime.telemetry.collector.TickQueue
import cyou.rime.telemetry.collector.TodayState
import cyou.rime.telemetry.collector.TodayText
import cyou.rime.telemetry.collector.WeekCell
import kotlinx.coroutines.flow.first
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.TextFieldValue
import cyou.rime.telemetry.collector.EditQueue
import kotlinx.coroutines.delay

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
    /** Under a habit with a rule: "Karantina İskelesi · 420 m away", or "reached 08:12". */
    ruleLines: Map<Long, String> = emptyMap(),
    /** True until the first try to reach the server is over: what is not saved yet shows as grey shapes. */
    waiting: Boolean = false,
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
            habits == null && waiting -> repeat(3) {
                Row(Modifier.fillMaxWidth().heightIn(min = 44.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Box(Modifier.size(20.dp).border(1.dp, colors.line))
                    PlaceholderBar(listOf(0.6f, 0.45f, 0.7f)[it])
                }
            }
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
                    ruleLines[habit.id]?.let { Small(it, Modifier.padding(start = 32.dp, bottom = 4.dp)) }
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
            budget == null && !state.budgetUnknown && waiting -> {
                PlaceholderLines(2)
                Gap(8.dp)
                ThinBar(0f, "")
            }
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
            now == null && waiting -> PlaceholderLines(1)
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

/** What the Habits screen changes, each saved on the phone at once and sent when the server can be reached. */
class HabitEditing(
    val rename: (Long, String) -> Unit,
    val weekdays: (Long, List<Int>) -> Unit,
    val archive: (Long, Boolean) -> Unit,
    val add: (String) -> Unit,
    val reasons: (List<String>) -> Unit,
    /** Opens the place picker for a habit's rule. */
    val pickRule: (Long) -> Unit = {},
    /** Removes a habit's rule. */
    val clearRule: (Long) -> Unit = {},
)

@Composable
fun HabitsScreen(
    state: HabitsState,
    stale: String?,
    notice: String?,
    editing: HabitEditing,
    /** Edit mode on the habits' rows, kept while a rule's place is picked. */
    editMode: Boolean = false,
    onEditMode: (Boolean) -> Unit = {},
    /** True until the first try to reach the server is over. */
    waiting: Boolean = false,
    /** Under a habit with a rule: "Karantina İskelesi · 420 m away". */
    ruleLines: Map<Long, String> = emptyMap(),
) = Page {
    PageHead("Habits")
    if (stale != null) {
        Small(stale)
        Gap(10.dp)
    }
    // The year first, each habit once on its own row; Edit makes those rows
    // the editor (owner 2026-11-27). The reasons come after.
    HabitsCard(state, notice, editing, editMode, onEditMode, waiting, ruleLines)
    ReasonsCard(state.reasons, editing.reasons, waiting && state.habits == null)
}

/** Monday first, as the panel lists them; 0 is Sunday. */
private val weekdayNames = listOf(1 to "Mon", 2 to "Tue", 3 to "Wed", 4 to "Thu", 5 to "Fri", 6 to "Sat", 0 to "Sun")

/** "every day", or the days in order: "Mon Wed Fri". */
fun weekdaysLabel(days: Set<Int>): String =
    if (days.isEmpty() || days.containsAll(listOf(0, 1, 2, 3, 4, 5, 6))) "every day" else weekdayNames.filter { it.first in days }.joinToString(" ") { it.second }

/**
 * All habits' year, then each habit once: its name, runs and rule line over
 * its year. In edit mode the same rows rename in place, open their days and
 * rule, and can be removed; a last row adds one.
 */
@Composable
private fun HabitsCard(
    state: HabitsState,
    notice: String?,
    editing: HabitEditing,
    editMode: Boolean,
    onEditMode: (Boolean) -> Unit,
    waiting: Boolean,
    ruleLines: Map<Long, String>,
) {
    val colors = panelColors()
    var naming by remember { mutableStateOf<Long?>(null) }
    var days by remember { mutableStateOf<Long?>(null) }
    var rules by remember { mutableStateOf<Long?>(null) }
    var adding by remember { mutableStateOf(false) }
    var removed by remember { mutableStateOf<Long?>(null) }
    LaunchedEffect(removed) {
        if (removed != null) {
            delay(5_000)
            removed = null
        }
    }
    val heatmap = state.heatmap
    val known = state.habits != null || heatmap != null
    val rows = remember(state.habits, heatmap) { HabitRows.merge(state.habits, heatmap) }
    PanelCard("Habits", action = if (!known) null else ({
        TinyAction(if (editMode) "Done" else "Edit") {
            naming = null
            days = null
            rules = null
            adding = false
            onEditMode(!editMode)
        }
    })) {
        if (!known) {
            if (waiting) YearPlaceholder() else Small("Unknown: the habits were never saved on this phone.", Modifier.padding(top = 6.dp))
            return@PanelCard
        }
        val layout = HabitCells.layout(state.openDay, 371)
        // One scroll for every row, so the weeks line up; it opens on the newest days.
        val scroll = rememberScrollState()
        LaunchedEffect(layout.weeks.size) {
            val end = snapshotFlow { scroll.maxValue }.first { it in 1 until Int.MAX_VALUE }
            scroll.scrollTo(end)
        }
        Gap(8.dp)
        when {
            heatmap != null -> {
                RowHead("All habits", "Share of the day's habits done", archived = false)
                MonthRow(layout, scroll)
                Grid(layout.weeks, scroll) { day ->
                    when (val level = HabitCells.level(heatmap.combined[day])) {
                        -1 -> CellState.UNKNOWN to null
                        0 -> CellState.MISSED to null
                        4 -> CellState.DONE to null
                        else -> CellState.DONE to lerp(colors.panel, colors.done, listOf(0f, .35f, .6f, .8f)[level])
                    }
                }
            }
            waiting -> YearPlaceholder()
            else -> Small("Unknown: the year was never saved on this phone.", Modifier.padding(top = 6.dp))
        }
        if (rows.isEmpty() && !adding && removed == null) Small("No habits yet. Once you add one, its days show here.", Modifier.padding(top = 10.dp))
        for (row in rows) {
            key(row.id) {
                val habit = row.def
                Column(Modifier.fillMaxWidth().padding(top = 14.dp)) {
                    if (editMode && habit != null) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (naming == habit.id) {
                                EditField(habit.name, "Name of ${habit.name}", 100, Modifier.weight(1f), onCancel = { naming = null }) { text ->
                                    naming = null
                                    val name = text.trim()
                                    if (removed != habit.id && name.isNotEmpty() && name != habit.name) editing.rename(habit.id, name)
                                }
                            } else {
                                Text(habit.name, Modifier.weight(1f).heightIn(min = 40.dp).clickable(role = Role.Button) { naming = habit.id; adding = false }
                                    .padding(top = 10.dp, bottom = 4.dp),
                                    style = TextStyle(fontFamily = Mono, fontSize = 15.sp, color = colors.fg, textDecoration = TextDecoration.Underline))
                            }
                            LinkWord("Remove") {
                                removed = habit.id
                                naming = null
                                days = null
                                rules = null
                                editing.archive(habit.id, true)
                            }
                        }
                        Small(weekdaysLabel(habit.weekdays), Modifier.fillMaxWidth().heightIn(min = 28.dp).clickable(role = Role.Button) { days = if (days == habit.id) null else habit.id }
                            .padding(vertical = 4.dp))
                        if (days == habit.id) DayToggles(habit.weekdays) { editing.weekdays(habit.id, it) }
                        // The rule: a place to go to, which ticks the habit by itself.
                        Small("Rule: ${cyou.rime.telemetry.collector.RuleText.summary(habit.rule)}", Modifier.fillMaxWidth().heightIn(min = 28.dp)
                            .clickable(role = Role.Button) { rules = if (rules == habit.id) null else habit.id }.padding(vertical = 4.dp))
                        if (rules == habit.id) {
                            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                LinkWord("Go to a place") { rules = null; editing.pickRule(habit.id) }
                                if (habit.rule != null) LinkWord("No rule") { rules = null; editing.clearRule(habit.id) }
                            }
                        }
                        Gap(4.dp)
                    } else {
                        RowHead(row.name, HabitCells.runs(row.year?.currentRun ?: 0, row.year?.longestRun ?: 0), row.archived)
                        ruleLines[row.id]?.let { Small(it, Modifier.padding(bottom = 3.dp)) }
                    }
                    val queued = TickQueue.doneByDay(state.queued, row.id)
                    Grid(layout.weeks, scroll) { day ->
                        (if (queued[day] == true) CellState.DONE else HabitCells.year(row.year?.days?.get(day), day, state.openDay)) to null
                    }
                }
            }
        }
        val gone = removed
        if (gone != null) {
            Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Small("Removed · ")
                Text("Undo", Modifier.heightIn(min = 36.dp).clickable(role = Role.Button) {
                    removed = null
                    editing.archive(gone, false)
                }.padding(vertical = 10.dp), style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, color = colors.muted, textDecoration = TextDecoration.Underline))
            }
        }
        if (editMode) {
            if (adding) {
                EditField("", "New habit", 100, Modifier.fillMaxWidth().padding(top = 6.dp), onCancel = { adding = false }) { text ->
                    adding = false
                    if (text.isNotBlank()) editing.add(text.trim())
                }
            } else {
                PText("+ Add a habit", Modifier.tapRow("Add a habit") { adding = true; naming = null }.padding(top = 12.dp), size = 15.sp)
            }
        }
        if (notice != null) Small(notice, Modifier.padding(top = 6.dp))
    }
}

/** A row's name and note: runs for a habit, what the shades mean for All habits. */
@Composable
private fun RowHead(name: String, note: String, archived: Boolean) {
    val colors = panelColors()
    // The whole name on its line, the runs under it (owner 2026-11-27: names were cut short).
    Column(Modifier.fillMaxWidth().padding(bottom = 3.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            PText(name, Modifier.weight(1f, fill = false), size = 13.5.sp, weight = FontWeight.Bold)
            if (archived) Text("ARCHIVED", Modifier.border(1.dp, colors.line).padding(horizontal = 4.dp), style = TextStyle(fontFamily = Mono, fontSize = 10.sp, color = colors.fg))
        }
        Small(note, color = colors.muted)
    }
}

/** Grey shapes where the year's rows go, while nothing is saved yet. */
@Composable
private fun YearPlaceholder() {
    val colors = panelColors()
    repeat(2) {
        Column(Modifier.fillMaxWidth().padding(top = 12.dp)) {
            PlaceholderBar(0.45f)
            Gap(6.dp)
            Box(Modifier.fillMaxWidth().height((CELL + CELL_GAP) * 7).background(colors.soft))
        }
    }
}

/** Seven square toggles; every day shows all seven on. The last day on stays on. */
@Composable
private fun DayToggles(current: Set<Int>, onChange: (List<Int>) -> Unit) {
    val colors = panelColors()
    val on = if (current.isEmpty()) setOf(0, 1, 2, 3, 4, 5, 6) else current
    Row(Modifier.fillMaxWidth().padding(bottom = 6.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        for ((day, name) in weekdayNames) {
            val active = day in on
            Box(
                Modifier.weight(1f).height(36.dp).background(if (active) colors.fg else colors.panel).border(1.dp, colors.fg)
                    .semantics { selected = active }
                    .clickable(role = Role.Checkbox) {
                        val next = if (active) on - day else on + day
                        if (next.isNotEmpty()) onChange(EditQueue.normalize(next))
                    },
                contentAlignment = Alignment.Center,
            ) {
                Text(name, style = TextStyle(fontFamily = Mono, fontSize = 11.sp, color = if (active) colors.bg else colors.fg, fontWeight = if (active) FontWeight.Bold else null))
            }
        }
    }
}

/** The reasons as text; Edit makes each edit in place (emptying one removes it) and a last row adds one. */
@Composable
private fun ReasonsCard(reasons: List<String>, onSave: (List<String>) -> Unit, waiting: Boolean) {
    var editMode by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<Int?>(null) }
    var adding by remember { mutableStateOf(false) }
    PanelCard("Reasons", action = if (waiting) null else ({
        TinyAction(if (editMode) "Done" else "Edit") {
            editing = null
            adding = false
            editMode = !editMode
        }
    })) {
        if (waiting) {
            PlaceholderLines(2)
            return@PanelCard
        }
        if (reasons.isEmpty() && !adding) Small("No reasons yet.", Modifier.padding(top = 6.dp))
        reasons.forEachIndexed { index, reason ->
            when {
                editMode && editing == index -> EditField(reason, "Reason", 500, Modifier.fillMaxWidth().padding(top = 6.dp), onCancel = { editing = null }) { text ->
                    editing = null
                    val line = text.trim()
                    if (line != reason) onSave(if (line.isEmpty()) reasons.filterIndexed { i, _ -> i != index } else reasons.mapIndexed { i, old -> if (i == index) line else old })
                }
                editMode -> Text(reason, Modifier.fillMaxWidth().heightIn(min = 40.dp).clickable(role = Role.Button) { editing = index; adding = false }.padding(top = 8.dp, bottom = 4.dp),
                    style = TextStyle(fontFamily = Mono, fontSize = 15.sp, color = panelColors().fg, textDecoration = TextDecoration.Underline, lineHeight = 15.sp * 1.35f))
                else -> PText(reason, Modifier.fillMaxWidth().padding(top = 8.dp, bottom = 4.dp), size = 15.sp)
            }
        }
        if (editMode) {
            if (adding) {
                EditField("", "New reason", 500, Modifier.fillMaxWidth().padding(top = 6.dp), onCancel = { adding = false }) { text ->
                    adding = false
                    if (text.isNotBlank()) onSave(reasons + text.trim())
                }
            } else {
                PText("+ Add a reason", Modifier.tapRow("Add a reason") { adding = true; editing = null }.padding(top = 12.dp), size = 15.sp)
            }
        }
    }
}

/**
 * Text edited in place, focused at once. Enter or leaving the field (another
 * place, another row) saves; Escape or back cancels.
 */
@Composable
private fun EditField(initial: String, label: String, maxLength: Int, modifier: Modifier = Modifier, onCancel: () -> Unit, onDone: (String) -> Unit) {
    val colors = panelColors()
    var value by remember { mutableStateOf(TextFieldValue(initial, TextRange(initial.length))) }
    val focus = remember { FocusRequester() }
    val closed = remember { booleanArrayOf(false) }
    var focused by remember { mutableStateOf(false) }
    val done by rememberUpdatedState(onDone)
    val cancel by rememberUpdatedState(onCancel)
    val text by rememberUpdatedState(value.text)
    fun finish(save: Boolean) {
        if (closed[0]) return
        closed[0] = true
        if (save) done(text) else cancel()
    }
    BackHandler { finish(false) }
    LaunchedEffect(Unit) { focus.requestFocus() }
    DisposableEffect(Unit) { onDispose { finish(true) } }
    BasicTextField(
        value = value,
        onValueChange = { if (it.text.length <= maxLength) value = it },
        modifier = modifier.focusRequester(focus)
            .onFocusChanged { if (it.isFocused) focused = true else if (focused) finish(true) }
            .onPreviewKeyEvent { event ->
                if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                when (event.key) {
                    Key.Escape -> { finish(false); true }
                    Key.Enter, Key.NumPadEnter -> { finish(true); true }
                    else -> false
                }
            }
            .semantics { contentDescription = label }
            .drawBehind { drawRect(colors.fg, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx())) }
            .padding(top = 10.dp, bottom = 6.dp),
        singleLine = true,
        textStyle = TextStyle(fontFamily = Mono, fontSize = 15.sp, color = colors.fg),
        cursorBrush = SolidColor(colors.fg),
        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences, imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { finish(true) }),
    )
}

private val CELL = 11.dp
private val CELL_GAP = 2.dp
private val LABELS = 26.dp
private val weekdayLabels = listOf("Mon", "", "Wed", "", "Fri", "", "")

/** The month names over the weeks, scrolled with the rows. */
@Composable
private fun MonthRow(layout: HabitCells.Layout, scroll: ScrollState) {
    val colors = panelColors()
    Row(Modifier.fillMaxWidth()) {
        Box(Modifier.width(LABELS))
        Box(Modifier.weight(1f).horizontalScroll(scroll)) {
            Box(Modifier.width((CELL + CELL_GAP) * layout.weeks.size).height(14.dp)) {
                for ((label, column) in layout.months) {
                    Small(label, Modifier.offset(x = (CELL + CELL_GAP) * column), color = colors.muted)
                }
            }
        }
    }
}

/** Seven rows of days, weeks as columns, with the weekday names kept at the left. */
@Composable
private fun Grid(weeks: List<List<String?>>, scroll: ScrollState, cell: (String) -> Pair<CellState, androidx.compose.ui.graphics.Color?>) {
    val colors = panelColors()
    val height = (CELL + CELL_GAP) * 7
    Row(Modifier.fillMaxWidth()) {
        Column(Modifier.width(LABELS)) {
            for (label in weekdayLabels) {
                Box(Modifier.size(LABELS - 2.dp, CELL + CELL_GAP), contentAlignment = Alignment.CenterStart) {
                    Text(label, style = TextStyle(fontFamily = Mono, fontSize = 9.sp, color = colors.muted, lineHeight = 9.sp))
                }
            }
        }
        Box(Modifier.weight(1f).horizontalScroll(scroll)) {
            Canvas(Modifier.width((CELL + CELL_GAP) * weeks.size).height(height)) {
                val step = (CELL + CELL_GAP).toPx()
                val side = CELL.toPx()
                weeks.forEachIndexed { column, week ->
                    week.forEachIndexed { row, day ->
                        if (day != null) {
                            val (state, fill) = cell(day)
                            habitCell(state, colors, Offset(column * step, row * step), Size(side, side), fill)
                        }
                    }
                }
            }
        }
    }
}
