package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import cyou.rime.telemetry.collector.CellState

// The panel's parts, drawn natively: square boxes with a 1px rule in the text
// colour, a 2px rule under each page title, small uppercase labels, and the
// panel's monospace type (web/src/lib/panel.css).

val Mono = FontFamily.Monospace

/** Body text, 16px as the panel's. */
@Composable
fun PText(text: String, modifier: Modifier = Modifier, color: Color = panelColors().fg, size: TextUnit = 16.sp, weight: FontWeight? = null, maxLines: Int = Int.MAX_VALUE) {
    Text(text, modifier, style = TextStyle(fontFamily = Mono, fontSize = size, color = color, fontWeight = weight, lineHeight = size * 1.35f), maxLines = maxLines)
}

/** The panel's .muted.small. */
@Composable
fun Small(text: String, modifier: Modifier = Modifier, color: Color = panelColors().muted) = PText(text, modifier, color = color, size = 12.5.sp)

/** A page title over a 2px rule, with a muted note on the right. */
@Composable
fun PageHead(title: String, note: String = "") {
    val colors = panelColors()
    Row(
        Modifier.fillMaxWidth().drawBehind {
            drawRect(colors.fg, Offset(0f, size.height - 2.dp.toPx()), Size(size.width, 2.dp.toPx()))
        }.padding(bottom = 10.dp),
        verticalAlignment = Alignment.Bottom,
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(title, style = TextStyle(fontFamily = Mono, fontSize = 23.sp, fontWeight = FontWeight.Bold, letterSpacing = (-0.04).em, color = colors.fg))
        if (note.isNotEmpty()) Small(note)
    }
    Spacer(Modifier.height(14.dp))
}

/** A square box with a 1px rule; [label] in small capitals and [note], or a small [action], on the right. */
@Composable
fun PanelCard(label: String, note: String = "", action: (@Composable () -> Unit)? = null, content: @Composable ColumnScope.() -> Unit) {
    val colors = panelColors()
    Column(
        Modifier.fillMaxWidth().padding(bottom = 12.dp).background(colors.panel).border(1.dp, colors.fg).padding(horizontal = 14.dp, vertical = 12.dp),
    ) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = if (action != null) Alignment.CenterVertically else Alignment.Bottom) {
            Text(label.uppercase(), style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, fontWeight = FontWeight.Bold, letterSpacing = 0.06.em, color = colors.fg))
            if (action != null) action() else if (note.isNotEmpty()) Small(note)
        }
        content()
    }
}

/** A small underlined word on a card's head that acts: Edit, Done. */
@Composable
fun TinyAction(text: String, onClick: () -> Unit) {
    val colors = panelColors()
    Text(
        text,
        Modifier.heightIn(min = 32.dp).clickable(role = Role.Button, onClick = onClick).padding(start = 12.dp, top = 8.dp, bottom = 8.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, color = colors.muted, textDecoration = androidx.compose.ui.text.style.TextDecoration.Underline),
    )
}

/** A grey bar where a line of text will go, while nothing is saved yet. */
@Composable
fun PlaceholderBar(share: Float, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth(share).height(10.dp).background(panelColors().soft))
}

/** Grey bars where [count] lines will go: the layout of the answer, with no words. */
@Composable
fun PlaceholderLines(count: Int, modifier: Modifier = Modifier) {
    val widths = floatArrayOf(0.82f, 0.56f, 0.7f, 0.44f, 0.64f, 0.5f)
    Column(modifier.fillMaxWidth().padding(top = 10.dp, bottom = 2.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        repeat(count) { PlaceholderBar(widths[it % widths.size]) }
    }
}

/** The panel's square checkbox: a 1px box that fills its middle when ticked. */
@Composable
fun SquareCheck(checked: Boolean, enabled: Boolean) {
    val colors = panelColors()
    Box(
        Modifier.size(20.dp).background(colors.panel).border(1.dp, if (enabled) colors.fg else colors.line),
        contentAlignment = Alignment.Center,
    ) {
        if (checked) Box(Modifier.size(12.dp).background(if (enabled) colors.fg else colors.muted))
    }
}

/** A row the whole width that can be tapped, 44dp high at least. */
fun Modifier.tapRow(label: String, enabled: Boolean = true, onClick: () -> Unit): Modifier =
    this.fillMaxWidth().heightIn(min = 44.dp).semantics { contentDescription = label }.clickable(enabled = enabled, role = Role.Button, onClick = onClick)

/** Draws one habit day as the panel does: done green, missed dark, before the habit dashed, unknown hatched. */
fun DrawScope.habitCell(state: CellState, colors: PanelColors, topLeft: Offset, cell: Size, fill: Color? = null) {
    val px = 1.dp.toPx()
    when (state) {
        CellState.DONE -> drawRect(fill ?: colors.done, topLeft, cell)
        CellState.MISSED -> {
            drawRect(colors.soft, topLeft, cell)
            drawRect(colors.line, topLeft + Offset(px / 2, px / 2), Size(cell.width - px, cell.height - px), style = Stroke(px))
        }
        CellState.NONE -> drawRect(colors.line, topLeft + Offset(px / 2, px / 2), Size(cell.width - px, cell.height - px),
            style = Stroke(px, pathEffect = PathEffect.dashPathEffect(floatArrayOf(2 * px, 2 * px))))
        CellState.UNSCHEDULED -> {
            val dot = 2.dp.toPx()
            drawRect(colors.line, topLeft + Offset(cell.width / 2 - dot / 2, cell.height / 2 - dot / 2), Size(dot, dot))
        }
        CellState.UNKNOWN -> clipRect(topLeft.x, topLeft.y, topLeft.x + cell.width, topLeft.y + cell.height) {
            val step = 4.dp.toPx()
            var x = -cell.height
            while (x < cell.width) {
                drawLine(colors.soft, Offset(topLeft.x + x, topLeft.y + cell.height), Offset(topLeft.x + x + cell.height, topLeft.y), 2.dp.toPx())
                x += step
            }
        }
        CellState.OPEN -> drawRect(colors.fg, topLeft + Offset(px / 2, px / 2), Size(cell.width - px, cell.height - px), style = Stroke(px))
    }
}

/** A thin meter, filled to [share]. */
@Composable
fun ThinBar(share: Float, label: String) {
    val colors = panelColors()
    Box(Modifier.fillMaxWidth().height(6.dp).background(colors.soft).border(1.dp, colors.line).semantics { contentDescription = label }) {
        if (share > 0f) Box(Modifier.fillMaxWidth(share).height(6.dp).background(colors.fg))
    }
}

@Composable
fun Gap(height: Dp) = Spacer(Modifier.height(height))
