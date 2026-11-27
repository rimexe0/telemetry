package cyou.rime.telemetry.collector

import android.app.AlarmManager
import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.LocalContext
import androidx.glance.LocalSize
import androidx.glance.action.Action
import androidx.glance.action.ActionParameters
import androidx.glance.action.actionParametersOf
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetManager
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.SizeMode
import androidx.glance.appwidget.action.ActionCallback
import androidx.glance.appwidget.action.actionRunCallback
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.lazy.LazyColumn
import androidx.glance.appwidget.lazy.items
import androidx.glance.appwidget.provideContent
import androidx.glance.appwidget.updateAll
import androidx.glance.background
import androidx.glance.color.ColorProvider
import androidx.glance.layout.Alignment
import androidx.glance.layout.Box
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxHeight
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.layout.size
import androidx.glance.layout.width
import androidx.glance.text.FontFamily
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import androidx.glance.unit.ColorProvider
import cyou.rime.telemetry.collector.ui.PanelDark
import cyou.rime.telemetry.collector.ui.PanelLight
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.ZoneId
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Today's habits on the home screen: the count and the distraction time left,
 * then one row per habit to tick. Drawn only from what the phone has saved,
 * so it works while the server is down; a tick goes into the same queue as
 * the app's ([HabitTicks]) and is sent from there. It never offers a door.
 */
class HabitWidget : GlanceAppWidget() {
    override val sizeMode: SizeMode = SizeMode.Exact

    override suspend fun provideGlance(context: Context, id: GlanceId) {
        provideContent {
            // Drawn again whenever the saved data changes (HabitWidgets.changed).
            HabitWidgets.version.collectAsState().value
            Content(HabitWidgets.read(LocalContext.current))
        }
    }

    @Composable
    private fun Content(state: WidgetState) {
        val context = LocalContext.current
        val wide = LocalSize.current.width >= 220.dp
        val openToday = actionStartActivity(HabitWidgets.openToday(context))
        // A thin border in the panel's line colour, square.
        Box(GlanceModifier.fillMaxSize().background(Colors.line).padding(1.dp)) {
            Column(GlanceModifier.fillMaxSize().background(Colors.bg).padding(horizontal = 12.dp, vertical = 8.dp)) {
                Column(GlanceModifier.fillMaxWidth().clickable(openToday).padding(bottom = 4.dp)) {
                    Text(state.header, style = text(Colors.fg, 13, bold = true), maxLines = 2)
                    state.saved?.let { Text(it, style = text(Colors.muted, 11), maxLines = 1) }
                }
                state.empty?.let { Text(it, GlanceModifier.fillMaxWidth().clickable(openToday).padding(vertical = 4.dp), style = text(Colors.muted, 12)) }
                val rows = state.rows.orEmpty()
                if (rows.isNotEmpty()) {
                    // Takes the height left, so many habits scroll.
                    LazyColumn(GlanceModifier.fillMaxWidth().defaultWeight()) {
                        items(rows, itemId = { it.id }) { row ->
                            HabitRow(row, state.ticksOpen, wide, if (state.ticksOpen) tick(row.id) else openToday)
                        }
                    }
                }
            }
        }
    }

    @Composable
    private fun HabitRow(row: WidgetRow, open: Boolean, wide: Boolean, onClick: Action) {
        Column(GlanceModifier.fillMaxWidth()) {
            Spacer(GlanceModifier.fillMaxWidth().height(4.dp))
            // The habit's last seven days behind its name (owner 2026-11-26).
            Box(GlanceModifier.fillMaxWidth().height(48.dp).clickable(onClick)) {
                Row(GlanceModifier.fillMaxSize()) {
                    row.week.forEachIndexed { index, cell ->
                        if (index > 0) Spacer(GlanceModifier.width(2.dp))
                        Spacer(GlanceModifier.defaultWeight().fillMaxHeight().background(cellColor(cell)))
                    }
                }
                Row(GlanceModifier.fillMaxSize().padding(horizontal = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    TickBox(row.done, open)
                    Spacer(GlanceModifier.width(10.dp))
                    Column(GlanceModifier.defaultWeight()) {
                        Text(row.name, style = text(if (row.done) Colors.muted else Colors.fg, 14), maxLines = 2)
                        row.rule?.let { Text(it, style = text(Colors.muted, 11), maxLines = 1) }
                    }
                    if (wide && row.worth.isNotEmpty()) Text(row.worth, GlanceModifier.padding(start = 8.dp), style = text(Colors.muted, 12), maxLines = 1)
                }
            }
        }
    }

    /** Done is the panel's green, toned down so the name stays readable; missed is the soft grey; the rest stays empty. */
    private fun cellColor(cell: CellState): ColorProvider = when (cell) {
        CellState.DONE -> Colors.doneCell
        CellState.MISSED -> Colors.soft
        CellState.OPEN -> Colors.panel
        else -> Colors.bg
    }

    /** The app's square tick box: a 1dp edge, filled in the middle when done. */
    @Composable
    private fun TickBox(done: Boolean, open: Boolean) {
        Box(GlanceModifier.size(18.dp).background(if (open) Colors.fg else Colors.line).padding(1.dp)) {
            Box(GlanceModifier.fillMaxSize().background(Colors.panel), contentAlignment = Alignment.Center) {
                if (done) Spacer(GlanceModifier.size(10.dp).background(if (open) Colors.fg else Colors.muted))
            }
        }
    }

    private fun tick(habitId: Long): Action = actionRunCallback<TickHabit>(actionParametersOf(TickHabit.HABIT to habitId))

    private fun text(color: ColorProvider, size: Int, bold: Boolean = false) =
        TextStyle(color = color, fontSize = size.sp, fontWeight = if (bold) FontWeight.Bold else FontWeight.Normal, fontFamily = FontFamily.Monospace)

    /** The panel's colours, light or dark as the phone is set. */
    private object Colors {
        private fun pick(light: Color, dark: Color) = ColorProvider(day = light, night = dark)
        val bg = pick(PanelLight.bg, PanelDark.bg)
        val panel = pick(PanelLight.panel, PanelDark.panel)
        val fg = pick(PanelLight.fg, PanelDark.fg)
        val muted = pick(PanelLight.muted, PanelDark.muted)
        val line = pick(PanelLight.line, PanelDark.line)
        val soft = pick(PanelLight.soft, PanelDark.soft)
        val doneCell = pick(blend(PanelLight.done, PanelLight.bg, .45f), blend(PanelDark.done, PanelDark.bg, .45f))

        /** [top] laid over [under] at [amount], as one opaque colour. */
        private fun blend(top: Color, under: Color, amount: Float) = Color(
            red = top.red * amount + under.red * (1 - amount),
            green = top.green * amount + under.green * (1 - amount),
            blue = top.blue * amount + under.blue * (1 - amount),
        )
    }
}

/** A tap on a habit row: ticked or unticked on the phone at once, then sent. */
class TickHabit : ActionCallback {
    companion object {
        val HABIT = ActionParameters.Key<Long>("habit")
    }

    override suspend fun onAction(context: Context, glanceId: GlanceId, parameters: ActionParameters) {
        val habitId = parameters[HABIT] ?: return
        withContext(Dispatchers.IO) {
            val tap = WidgetModel.tap(Saved.read(context), habitId, Instant.now(), ZoneId.systemDefault()) ?: return@withContext
            // Saving it draws the widget again from the queue (HabitTicks → HabitWidgets.changed).
            HabitTicks.record(context, habitId, tap.first, tap.second)
            HabitWidget().update(context, glanceId)
            // Relay first, or later from the collection cycle when the server is back.
            runCatching { HabitTicks.flush(context) }
        }
    }
}

class HabitWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = HabitWidget()

    override fun onUpdate(context: Context, appWidgetManager: AppWidgetManager, appWidgetIds: IntArray) {
        super.onUpdate(context, appWidgetManager, appWidgetIds)
        HabitWidgets.schedule(context)
    }

    override fun onDisabled(context: Context) {
        super.onDisabled(context)
        HabitWidgets.cancelClock(context)
    }
}

/** Draws the widget again when the day starts or bedtime closes the ticks. It never wakes the phone. */
class HabitWidgetClock : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val pending = goAsync()
        HabitWidgets.scope.launch {
            try {
                HabitWidgets.update(context.applicationContext)
            } finally {
                pending.finish()
            }
        }
    }
}

/** Keeps the widget in step with what the phone has saved. */
object HabitWidgets {
    /** Opens the app on Today. */
    const val OPEN_TODAY = "cyou.rime.telemetry.collector.OPEN_TODAY"

    internal val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val _version = MutableStateFlow(0L)
    /** Raised each time the saved data changes; the widget and the open app redraw on it. */
    val version: StateFlow<Long> = _version
    private val pending = AtomicBoolean(false)

    fun openToday(context: Context): Intent =
        Intent(context, MainActivity::class.java).setAction(OPEN_TODAY).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    fun read(context: Context): WidgetState {
        val saved = Saved.read(context)
        val savedAt = NowCache.read(context)?.fetchedAt ?: HabitCache.read(context, HabitCache.LIST)?.fetchedAt
        return WidgetModel.state(saved, savedAt, Instant.now(), ZoneId.systemDefault(), HabitRuleWatch.latest(context))
    }

    /**
     * The saved data changed: the widget is drawn again shortly. Calls close
     * together are drawn once. Cheap when no widget is placed.
     */
    fun changed(context: Context) {
        _version.update { it + 1 }
        if (!pending.compareAndSet(false, true)) return
        val app = context.applicationContext
        scope.launch {
            delay(300)
            pending.set(false)
            update(app)
        }
    }

    /** Draws every placed widget and sets the next day-start clock. */
    suspend fun update(context: Context) {
        if (!placed(context)) return cancelClock(context)
        _version.update { it + 1 }
        runCatching { HabitWidget().updateAll(context) }
        schedule(context)
    }

    private suspend fun placed(context: Context): Boolean =
        runCatching { GlanceAppWidgetManager(context).getGlanceIds(HabitWidget::class.java).isNotEmpty() }.getOrDefault(false)

    /** A clock that does not wake the phone: it rings at the next day start or the close of today's ticks, or when the phone next wakes after. */
    fun schedule(context: Context) {
        runCatching {
            val at = WidgetModel.nextChange(Saved.read(context), Instant.now(), ZoneId.systemDefault())
            context.getSystemService(AlarmManager::class.java).set(AlarmManager.RTC, at.toEpochMilli() + 1_000, clock(context))
        }
    }

    fun cancelClock(context: Context) {
        runCatching { context.getSystemService(AlarmManager::class.java).cancel(clock(context)) }
    }

    private fun clock(context: Context): PendingIntent = PendingIntent.getBroadcast(
        context, 0, Intent(context, HabitWidgetClock::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )
}
