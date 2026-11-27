package cyou.rime.telemetry.collector.ui

import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Point
import android.graphics.Typeface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.viewinterop.AndroidView
import cyou.rime.telemetry.collector.Places
import cyou.rime.telemetry.collector.PlacesDay
import cyou.rime.telemetry.collector.ServerTiles
import org.osmdroid.config.Configuration
import org.osmdroid.util.BoundingBox
import org.osmdroid.util.GeoPoint
import org.osmdroid.views.CustomZoomButtonsController
import org.osmdroid.views.MapView
import org.osmdroid.views.overlay.Overlay
import java.io.File

/**
 * The day's map, as the panel's Places draws it: the path between stays, broken where
 * positions are more than 15 minutes apart, the stays
 * with their times, home, and each habit's place as a circle, green once
 * reached. Tiles come only through the owner's server ([ServerTiles]).
 */
@Composable
fun PlacesMap(day: PlacesDay, modifier: Modifier, period: Boolean = false) {
    val context = LocalContext.current
    val colors = panelColors()
    val density = LocalDensity.current.density
    val map = remember {
        val config = Configuration.getInstance()
        // Names osmdroid's own files; it never downloads anything here.
        config.userAgentValue = "Telemetry"
        val base = File(context.cacheDir, "osmdroid")
        config.osmdroidBasePath = base
        config.osmdroidTileCache = File(base, "tiles")
        MapView(context, ServerTiles.provider(context)).apply {
            // The tiles stay inside the map's box (owner 2026-11-27: they covered the page).
            clipToOutline = true
            setMultiTouchControls(true)
            zoomController.setVisibility(CustomZoomButtonsController.Visibility.NEVER)
            setBackgroundColor(colors.soft.toArgb())
            controller.setZoom(14.0)
        }
    }
    DisposableEffect(map) {
        map.onResume()
        onDispose { map.onPause(); map.onDetach() }
    }
    val fitted = remember { arrayOf<String?>(null) }
    AndroidView(
        factory = { map },
        modifier = modifier.semantics { contentDescription = "Map of the day's places" },
        update = { view ->
            view.overlays.clear()
            view.overlays.add(DayOverlay(day, colors, density, period))
            view.invalidate()
            // Fitted to the day once per day shown, so a refresh never moves the map under a finger.
            if (fitted[0] != day.date) {
                fitted[0] = day.date
                val points = Places.usable(day.positions).map { GeoPoint(it.lat, it.lon) } + day.stays.map { GeoPoint(it.lat, it.lon) }
                if (points.isNotEmpty()) {
                    val fit = {
                        if (points.size == 1) {
                            view.controller.setZoom(16.0)
                            view.controller.setCenter(points[0])
                        } else {
                            val box = BoundingBox.fromGeoPoints(points)
                            view.zoomToBoundingBox(box.increaseByScale(1.25f), false, (28 * density).toInt())
                            if (view.zoomLevelDouble > 17.0) view.controller.setZoom(17.0)
                        }
                    }
                    if (view.width > 0 && view.height > 0) fit() else view.addOnFirstLayoutListener { _, _, _, _, _ -> fit() }
                }
            }
        },
    )
}

/**
 * Everything drawn over the tiles, in screen pixels. For a week, month or
 * year ([period]) there is no path: the positions are dots, if any, and
 * each place stayed at is labelled once, by its name.
 */
private class DayOverlay(private val day: PlacesDay, colors: PanelColors, private val density: Float, private val period: Boolean = false) : Overlay() {
    private val fg = colors.fg.toArgb()
    private val bg = colors.bg.toArgb()
    private val muted = colors.muted.toArgb()
    private val done = colors.done.toArgb()
    private val points = Places.usable(day.positions)
    private val lines = if (period) emptyList() else Places.pathLines(points, day.stays)
    private val visits = Places.visits(points, day.rules)

    private val line = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeWidth = 2 * density; color = fg; alpha = 170; strokeJoin = Paint.Join.ROUND; strokeCap = Paint.Cap.ROUND }
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
    private val stroke = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeWidth = 2 * density }
    private val text = Paint(Paint.ANTI_ALIAS_FLAG).apply { typeface = Typeface.MONOSPACE; textSize = 11 * density; color = fg }
    private val box = Paint().apply { style = Paint.Style.FILL; color = bg }
    private val boxLine = Paint().apply { style = Paint.Style.STROKE; strokeWidth = density; color = fg }
    private val at = Point()

    override fun draw(canvas: Canvas, mapView: MapView, shadow: Boolean) {
        if (shadow) return
        val projection = mapView.projection
        fun pixel(lat: Double, lon: Double): Point = projection.toPixels(GeoPoint(lat, lon), at).let { Point(it.x, it.y) }

        // The habits' places: a circle of their radius, green once reached.
        for (place in day.rules) {
            val reached = visits.any { it.place.habitId == place.habitId } ||
                (period && day.stays.any { cyou.rime.telemetry.collector.PlaceRule.meters(it.lat, it.lon, place.lat, place.lon) <= place.radiusM })
            val center = pixel(place.lat, place.lon)
            val edge = pixel(place.lat + place.radiusM / 111_320.0, place.lon)
            val radius = kotlin.math.abs(center.y - edge.y).toFloat().coerceAtLeast(4 * density)
            fill.color = if (reached) done else muted
            fill.alpha = 30
            canvas.drawCircle(center.x.toFloat(), center.y.toFloat(), radius, fill)
            stroke.color = if (reached) done else muted
            canvas.drawCircle(center.x.toFloat(), center.y.toFloat(), radius, stroke)
            label(canvas, place.name, center.x.toFloat() - text.measureText(place.name) / 2, center.y - radius - 6 * density)
        }
        // The path, broken at gaps over 15 minutes.
        for (part in lines) {
            if (part.size < 2) continue
            val path = android.graphics.Path()
            part.forEachIndexed { index, fix ->
                val p = pixel(fix.lat, fix.lon)
                if (index == 0) path.moveTo(p.x.toFloat(), p.y.toFloat()) else path.lineTo(p.x.toFloat(), p.y.toFloat())
            }
            canvas.drawPath(path, line)
        }
        // A day draws no dot per position: at home they pile into a blob; the line says enough.
        if (period) {
            fill.color = fg
            fill.alpha = 110
            for (fix in points) {
                val p = pixel(fix.lat, fix.lon)
                canvas.drawCircle(p.x.toFloat(), p.y.toFloat(), 1.5f * density, fill)
            }
        }
        // Home, filled; each other stay, hollow, with its times and place.
        day.stays.firstOrNull { it.home }?.let { home ->
            val p = pixel(home.lat, home.lon)
            fill.color = fg
            fill.alpha = 255
            canvas.drawCircle(p.x.toFloat(), p.y.toFloat(), 7 * density, fill)
            label(canvas, "home", p.x + 10 * density, p.y + 4 * density)
        }
        val named = mutableSetOf<String>()
        for (stay in day.stays) {
            if (stay.home) continue
            val p = pixel(stay.lat, stay.lon)
            fill.color = bg
            fill.alpha = 255
            canvas.drawCircle(p.x.toFloat(), p.y.toFloat(), 6 * density, fill)
            stroke.color = fg
            canvas.drawCircle(p.x.toFloat(), p.y.toFloat(), 6 * density, stroke)
            if (!period) label(canvas, Places.stayLabel(stay, visits, day.zone), p.x + 10 * density, p.y + 4 * density)
            else Places.stayName(stay, visits).takeIf { it.isNotEmpty() && named.add(it) }?.let { label(canvas, it, p.x + 10 * density, p.y + 4 * density) }
        }
    }

    /** A label in a square box with a 1px rule, as the panel's map labels. */
    private fun label(canvas: Canvas, words: String, x: Float, baseline: Float) {
        val width = text.measureText(words)
        val pad = 3 * density
        canvas.drawRect(x - pad, baseline - text.textSize, x + width + pad, baseline + pad, box)
        canvas.drawRect(x - pad, baseline - text.textSize, x + width + pad, baseline + pad, boxLine)
        canvas.drawText(words, x, baseline, text)
    }
}
