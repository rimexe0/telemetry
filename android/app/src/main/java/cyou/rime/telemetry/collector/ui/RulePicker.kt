package cyou.rime.telemetry.collector.ui

import android.content.Context
import android.graphics.Color as AndroidColor
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import cyou.rime.telemetry.collector.HabitRule
import cyou.rime.telemetry.collector.PlaceRule
import cyou.rime.telemetry.collector.PlaceSearch
import cyou.rime.telemetry.collector.RuleDraft
import cyou.rime.telemetry.collector.RuleFix
import cyou.rime.telemetry.collector.RuleText
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.osmdroid.config.Configuration
import org.osmdroid.events.MapEventsReceiver
import org.osmdroid.tileprovider.tilesource.TileSourceFactory
import org.osmdroid.util.GeoPoint
import org.osmdroid.views.CustomZoomButtonsController
import org.osmdroid.views.MapView
import org.osmdroid.views.overlay.MapEventsOverlay
import org.osmdroid.views.overlay.Marker
import org.osmdroid.views.overlay.Polygon
import java.io.File

/**
 * The habit rule's place picker: a map of OpenStreetMap tiles with the
 * phone's last position and the chosen place, a search by name, and a tap
 * on the map to pick. The distance, the window and the minutes are set
 * here too. Nothing is saved until Save.
 */
@Composable
fun RulePicker(habitName: String, current: HabitRule?, latest: RuleFix?, onSave: (HabitRule) -> Unit, onCancel: () -> Unit) {
    val colors = panelColors()
    val scope = rememberCoroutineScope()
    // A new rule starts from the window "from waking until 12:00", shown and changeable.
    var draft by remember { mutableStateOf(if (current == null) RuleDraft(until = "12:00") else RuleDraft.of(current)) }
    var query by remember { mutableStateOf("") }
    var found by remember { mutableStateOf<List<PlaceSearch.Found>?>(null) }
    var searching by remember { mutableStateOf(false) }
    var searchNote by remember { mutableStateOf<String?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    BackHandler(onBack = onCancel)

    fun search() {
        val words = query.trim()
        if (words.isEmpty() || searching) return
        searching = true
        searchNote = null
        scope.launch {
            val result = withContext(Dispatchers.IO) { PlaceSearch.search(words, latest) }
            searching = false
            found = result
            searchNote = when {
                result == null -> "The search can't be reached. Tap the map instead."
                result.isEmpty() -> "Nothing found by that name."
                else -> null
            }
        }
    }

    Column(Modifier.fillMaxSize().background(colors.bg).verticalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, top = 16.dp, bottom = 24.dp)) {
        PageHead("Rule", habitName)
        PanelCard("Go to a place") {
            Row(Modifier.fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Field(query, "Search by name", Modifier.weight(1f), onDone = { search() }) { query = it }
                Word(if (searching) "Searching…" else "Search", enabled = !searching) { search() }
            }
            searchNote?.let { Small(it, Modifier.padding(top = 4.dp)) }
            found.orEmpty().forEach { place ->
                Column(Modifier.tapRow("${place.name}, ${place.detail}") {
                    draft = draft.copy(name = place.name, lat = place.lat, lon = place.lon)
                    found = null
                }.padding(vertical = 6.dp)) {
                    PText(place.name, size = 14.sp)
                    if (place.detail.isNotEmpty()) Small(place.detail)
                }
            }
            Gap(8.dp)
            PlaceMap(draft, latest) { lat, lon ->
                draft = draft.copy(lat = lat, lon = lon, name = draft.name.ifBlank { "Picked place" })
            }
            Small(
                when {
                    draft.lat == null -> if (latest == null) "Tap the map to pick the place. This phone's position is unknown." else "Tap the map to pick the place. The square is this phone."
                    latest != null -> "${RuleText.distance(PlaceRule.meters(latest.lat, latest.lon, draft.lat!!, draft.lon!!))} from this phone's last position."
                    else -> "This phone's position is unknown."
                },
                Modifier.padding(top = 6.dp),
            )
            Labeled("Name") { Field(draft.name, "Name of the place", Modifier.fillMaxWidth()) { draft = draft.copy(name = it.take(100)) } }
            Labeled("Within (m)") { Field(draft.radius, "Distance in meters", Modifier.width(96.dp), number = true) { draft = draft.copy(radius = it.filter(Char::isDigit).take(4)) } }
            Labeled("For (min)") { Field(draft.minutes, "Minutes there", Modifier.width(96.dp), number = true) { draft = draft.copy(minutes = it.filter(Char::isDigit).take(3)) } }
        }
        PanelCard("When") {
            Row(Modifier.fillMaxWidth().padding(top = 6.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                Choice("From waking", draft.fromWake, Modifier.weight(1f)) { draft = draft.copy(fromWake = true) }
                Choice("From a time", !draft.fromWake, Modifier.weight(1f)) { draft = draft.copy(fromWake = false, from = draft.from.ifBlank { "07:00" }) }
            }
            if (!draft.fromWake) Labeled("From") { Field(draft.from, "From, HH:MM", Modifier.width(96.dp), time = true) { draft = draft.copy(from = it.take(5)) } }
            Labeled("Until") { Field(draft.until, "Until, HH:MM", Modifier.width(96.dp), time = true) { draft = draft.copy(until = it.take(5)) } }
            if (draft.fromWake) Small("Before the wake shows in the records, the window opens at the end of bedtime.", Modifier.padding(top = 4.dp))
        }
        problem?.let { Small(it, Modifier.padding(top = 4.dp), color = colors.fg) }
        Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            Word("Save", bold = true) {
                val (rule, missing) = draft.rule()
                if (rule != null) onSave(rule) else problem = missing
            }
            Word("Cancel", onClick = onCancel)
        }
    }
}

@Composable
private fun Labeled(label: String, content: @Composable () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Small(label, Modifier.width(96.dp))
        Box(Modifier.weight(1f)) { content() }
    }
}

/** Two square choices side by side, the one in force filled. */
@Composable
private fun Choice(label: String, active: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val colors = panelColors()
    Box(
        modifier.height(40.dp).background(if (active) colors.fg else colors.panel).border(1.dp, colors.fg)
            .semantics { selected = active }.clickable(role = Role.RadioButton, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = TextStyle(fontFamily = Mono, fontSize = 12.sp, color = if (active) colors.bg else colors.fg, fontWeight = if (active) FontWeight.Bold else null))
    }
}

@Composable
private fun Word(text: String, enabled: Boolean = true, bold: Boolean = false, onClick: () -> Unit) {
    val colors = panelColors()
    Text(
        text,
        Modifier.heightIn(min = 44.dp).clickable(enabled = enabled, role = Role.Button, onClick = onClick).padding(horizontal = 4.dp, vertical = 12.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 13.sp, color = colors.fg, textDecoration = TextDecoration.Underline, fontWeight = if (bold) FontWeight.Bold else null),
    )
}

@Composable
private fun Field(value: String, label: String, modifier: Modifier, number: Boolean = false, time: Boolean = false, onDone: () -> Unit = {}, onChange: (String) -> Unit) {
    val colors = panelColors()
    BasicTextField(
        value = value,
        onValueChange = onChange,
        modifier = modifier.semantics { contentDescription = label }
            .drawBehind { drawRect(colors.fg, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx())) }
            .padding(top = 10.dp, bottom = 6.dp),
        singleLine = true,
        textStyle = TextStyle(fontFamily = Mono, fontSize = 15.sp, color = colors.fg),
        cursorBrush = SolidColor(colors.fg),
        keyboardOptions = KeyboardOptions(
            keyboardType = when {
                number -> KeyboardType.Number
                time -> KeyboardType.Ascii
                else -> KeyboardType.Text
            },
            imeAction = ImeAction.Done,
        ),
        keyboardActions = KeyboardActions(onDone = { onDone() }),
    )
}

/** Sets osmdroid up once: the app's generic name as User-Agent, tiles cached in the app's own cache. */
private fun setUpMaps(context: Context) {
    val config = Configuration.getInstance()
    config.userAgentValue = PlaceSearch.USER_AGENT
    val base = File(context.cacheDir, "osmdroid")
    config.osmdroidBasePath = base
    config.osmdroidTileCache = File(base, "tiles")
    // The disk is small: a few days of looking around the neighbourhood.
    config.tileFileSystemCacheMaxBytes = 40L * 1024 * 1024
    config.tileFileSystemCacheTrimBytes = 30L * 1024 * 1024
}

/** The map: the place with its circle, the phone as a square, a tap picks. */
@Composable
private fun PlaceMap(draft: RuleDraft, latest: RuleFix?, onPick: (Double, Double) -> Unit) {
    val colors = panelColors()
    val context = LocalContext.current
    val fg = colors.fg.toArgb()
    val map = remember {
        setUpMaps(context)
        MapView(context).apply {
            setTileSource(TileSourceFactory.MAPNIK)
            setMultiTouchControls(true)
            zoomController.setVisibility(CustomZoomButtonsController.Visibility.NEVER)
            val center = when {
                draft.lat != null && draft.lon != null -> GeoPoint(draft.lat, draft.lon)
                latest != null -> GeoPoint(latest.lat, latest.lon)
                else -> null
            }
            if (center != null) {
                controller.setZoom(16.0)
                controller.setCenter(center)
            } else controller.setZoom(3.0)
        }
    }
    DisposableEffect(map) {
        map.onResume()
        onDispose { map.onPause(); map.onDetach() }
    }
    val pick by androidx.compose.runtime.rememberUpdatedState(onPick)
    // The place last shown: a place chosen by name moves the map to it; a tap does not.
    val shown = remember { arrayOf<Pair<Double, Double>?>(draft.lat?.let { lat -> draft.lon?.let { lat to it } }) }
    AndroidView(
        factory = { map },
        modifier = Modifier.fillMaxWidth().height(300.dp).border(1.dp, colors.line).semantics { contentDescription = "Map. Tap to pick the place." },
        update = { view ->
            view.overlays.clear()
            view.overlays.add(MapEventsOverlay(object : MapEventsReceiver {
                override fun singleTapConfirmedHelper(point: GeoPoint): Boolean {
                    pick(point.latitude, point.longitude)
                    return true
                }

                override fun longPressHelper(point: GeoPoint): Boolean = false
            }))
            if (latest != null) {
                view.overlays.add(Polygon(view).apply {
                    points = Polygon.pointsAsRect(GeoPoint(latest.lat, latest.lon), 14.0, 14.0).map { it as GeoPoint }
                    fillPaint.color = fg
                    outlinePaint.color = fg
                    setOnClickListener { _, _, _ -> false }
                })
            }
            val lat = draft.lat
            val lon = draft.lon
            if (lat != null && lon != null) {
                if (shown[0] != (lat to lon)) {
                    val visible = view.boundingBox.contains(lat, lon)
                    shown[0] = lat to lon
                    if (!visible) {
                        view.controller.setZoom(16.0)
                        view.controller.animateTo(GeoPoint(lat, lon))
                    }
                }
                val radius = draft.radius.toDoubleOrNull()?.coerceIn(20.0, 2000.0) ?: 100.0
                view.overlays.add(Polygon(view).apply {
                    points = Polygon.pointsAsCircle(GeoPoint(lat, lon), radius)
                    fillPaint.color = AndroidColor.argb(40, AndroidColor.red(fg), AndroidColor.green(fg), AndroidColor.blue(fg))
                    outlinePaint.color = fg
                    outlinePaint.strokeWidth = 3f
                    setOnClickListener { _, _, _ -> false }
                })
                view.overlays.add(Marker(view).apply {
                    position = GeoPoint(lat, lon)
                    setAnchor(Marker.ANCHOR_CENTER, Marker.ANCHOR_BOTTOM)
                    title = draft.name
                    setOnMarkerClickListener { _, _ -> false }
                })
            }
            view.invalidate()
        },
    )
}
