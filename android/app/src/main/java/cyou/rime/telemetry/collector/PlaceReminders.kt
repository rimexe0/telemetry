package cyou.rime.telemetry.collector

import android.content.Context
import android.net.wifi.WifiManager
import org.json.JSONObject
import java.time.Instant
import java.util.concurrent.Executors
import kotlin.math.asin
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.sin
import kotlin.math.sqrt

/** A circle around a place: inside when within radius meters. */
data class PlacePoint(val lat: Double, val lon: Double, val radius: Double)

/** Where a follow-up comes due: on arriving at or leaving any of the points. */
data class ReminderPlace(val arrive: Boolean, val name: String, val home: Boolean, val points: List<PlacePoint>) {
    /** How the reminder reads before it comes due, such as "at home" or "leaving Migros". */
    val label: String get() = if (arrive) "at $name" else "leaving $name"

    companion object {
        fun parse(json: JSONObject?): ReminderPlace? {
            json ?: return null
            val array = json.optJSONArray("points") ?: return null
            val points = (0 until array.length()).mapNotNull { index ->
                val point = array.optJSONObject(index) ?: return@mapNotNull null
                PlacePoint(point.optDouble("lat"), point.optDouble("lon"), point.optDouble("radius")).takeIf { !it.lat.isNaN() && !it.lon.isNaN() && it.radius > 0 }
            }
            if (points.isEmpty()) return null
            return ReminderPlace(json.optString("when") != "leave", json.optString("name", "the place"), json.optBoolean("home"), points)
        }
    }
}

/**
 * When a place follow-up comes due, without Android, so it can be tested.
 * Inside means within a point's radius; outside means more than 100 m past
 * it, so GPS wobble at the edge does not count as coming and going. An
 * arrival counts only after the phone was seen outside since the reminder
 * was set (said at home, "when I get home" waits for the next time), and a
 * departure only after it was seen inside.
 */
object PlaceRule {
    enum class Where { IN, OUT, UNSURE }

    /** What the phone has seen since the reminder was set. */
    data class Seen(val wasIn: Boolean, val wasOut: Boolean)

    private const val EDGE = 100.0
    private const val WORST_ACCURACY = 100.0

    fun where(place: ReminderPlace, lat: Double?, lon: Double?, accuracy: Double?, onHomeWifi: Boolean): Where {
        if (place.home && onHomeWifi) return Where.IN
        if (lat == null || lon == null || (accuracy ?: 0.0) > WORST_ACCURACY) return Where.UNSURE
        val past = place.points.minOf { meters(lat, lon, it.lat, it.lon) - it.radius }
        return when {
            past <= 0 -> Where.IN
            past > EDGE -> Where.OUT
            else -> Where.UNSURE
        }
    }

    /** The first look, when the reminder is new to this phone. Unsure counts as away for an arrival. */
    fun first(place: ReminderPlace, where: Where): Seen =
        Seen(wasIn = where == Where.IN, wasOut = where == Where.OUT || (place.arrive && where == Where.UNSURE))

    fun update(seen: Seen, where: Where): Seen = Seen(seen.wasIn || where == Where.IN, seen.wasOut || where == Where.OUT)

    /** Whether it comes due now, given what was seen before this look. */
    fun fires(place: ReminderPlace, before: Seen, where: Where, now: Instant, dueAt: Instant): Boolean =
        !now.isBefore(dueAt) && if (place.arrive) where == Where.IN && before.wasOut else where == Where.OUT && before.wasIn

    fun meters(lat1: Double, lon1: Double, lat2: Double, lon2: Double): Double {
        val radians = Math.PI / 180
        val a = sin((lat2 - lat1) * radians / 2).pow(2) + cos(lat1 * radians) * cos(lat2 * radians) * sin((lon2 - lon1) * radians / 2).pow(2)
        return 2 * 6_371_000.0 * asin(sqrt(a))
    }
}

/**
 * Checks place follow-ups on the phone itself, on every position and every
 * Wi-Fi change, so a reminder comes due on arrival even offline. Joining the
 * home Wi-Fi counts as arriving home: GPS often drops out indoors, right as
 * the owner walks in. The home Wi-Fi is learned, never asked for: the network
 * the phone is on while its position is at home. The server is told when one
 * fires, now or when it is reachable.
 */
object PlaceReminders {
    private const val PREFS = "telemetry-place-reminders"
    private const val KEY_LAST = "last_position"
    private const val KEY_HOME = "home"
    private const val KEY_HOME_WIFI = "home_wifi"
    private const val KEY_SEEN = "seen:"
    private const val KEY_FIRED = "fired:"
    private const val KEY_UNREPORTED = "unreported"
    private const val LEARN_METERS = 80.0
    private const val LEARN_ACCURACY = 50.0

    private val worker = Executors.newSingleThreadExecutor()

    fun onPosition(context: Context, lat: Double, lon: Double, accuracy: Double) {
        val app = context.applicationContext
        worker.execute {
            prefs(app).edit().putString(KEY_LAST, "$lat,$lon,$accuracy").apply()
            runCatching { evaluate(app, notify = true) }
        }
    }

    /** After a Wi-Fi change or anything else that may matter, from any thread. */
    fun checkSoon(context: Context) {
        val app = context.applicationContext
        worker.execute { runCatching { evaluate(app, notify = true) } }
    }

    /** When this phone saw the follow-up's place reached, or null. */
    fun firedAt(context: Context, id: Long): Instant? =
        prefs(context).getString(KEY_FIRED + id, null)?.let { runCatching { Instant.parse(it) }.getOrNull() }

    /**
     * Looks at every waiting place follow-up in the cached server answer.
     * Blocking IO when one fires; call it off the main thread.
     */
    @Synchronized
    fun evaluate(context: Context, notify: Boolean) {
        val json = NowCache.read(context)?.json
        val listed = json?.let { runCatching { FollowUpWatch.parse(it) }.getOrNull() }.orEmpty()
        val preferences = prefs(context)
        val editor = preferences.edit()
        val last = preferences.getString(KEY_LAST, null)?.split(",")?.mapNotNull { it.toDoubleOrNull() }?.takeIf { it.size == 3 }
        listed.firstOrNull { it.place?.home == true }?.place?.points?.firstOrNull()?.let { editor.putString(KEY_HOME, "${it.lat},${it.lon}") }
        val wifi = wifiName(context)
        val homeWifi = learnHomeWifi(preferences, editor, last, wifi)
        val onHomeWifi = wifi != null && wifi in homeWifi
        val now = Instant.now()
        var unreported = unreported(preferences)
        var fired = false
        val waiting = listed.filter { it.place != null && it.triggeredAt == null }
        for (followUp in waiting) {
            val place = followUp.place ?: continue
            if (preferences.contains(KEY_FIRED + followUp.id)) continue
            val where = PlaceRule.where(place, last?.get(0), last?.get(1), last?.get(2), onHomeWifi)
            val before = preferences.getString(KEY_SEEN + followUp.id, null)?.let(::parseSeen)
            if (before == null) {
                editor.putString(KEY_SEEN + followUp.id, seenText(PlaceRule.first(place, where)))
                continue
            }
            if (PlaceRule.fires(place, before, where, now, followUp.dueAt)) {
                editor.putString(KEY_FIRED + followUp.id, now.toString())
                unreported = unreported + followUp.id.toString()
                editor.putStringSet(KEY_UNREPORTED, unreported)
                fired = true
            } else {
                editor.putString(KEY_SEEN + followUp.id, seenText(PlaceRule.update(before, where)))
            }
        }
        // Forget follow-ups the server no longer lists as open.
        val open = listed.map { it.id }.toSet()
        for (key in preferences.all.keys) {
            val id = when {
                key.startsWith(KEY_SEEN) -> key.removePrefix(KEY_SEEN)
                key.startsWith(KEY_FIRED) -> key.removePrefix(KEY_FIRED)
                else -> continue
            }.toLongOrNull() ?: continue
            if (json != null && id !in open) editor.remove(key)
        }
        editor.commit()
        if (fired && notify) runCatching { FollowUpWatch.check(context, json) }
        report(context)
    }

    private fun report(context: Context) {
        val preferences = prefs(context)
        val pending = unreported(preferences)
        if (pending.isEmpty()) return
        val sent = pending.filter { id ->
            val at = preferences.getString(KEY_FIRED + id, null)
            DeviceApi.post(context, "/v1/device/follow-ups/$id/triggered", JSONObject().put("at", at ?: Instant.now().toString()).toString())
        }.toSet()
        if (sent.isNotEmpty()) preferences.edit().putStringSet(KEY_UNREPORTED, unreported(prefs(context)) - sent).commit()
    }

    /** The Wi-Fi seen while the position is at home becomes the home Wi-Fi. */
    private fun learnHomeWifi(preferences: android.content.SharedPreferences, editor: android.content.SharedPreferences.Editor, last: List<Double>?, wifi: String?): Set<String> {
        val known = preferences.getStringSet(KEY_HOME_WIFI, emptySet()).orEmpty()
        val home = preferences.getString(KEY_HOME, null)?.split(",")?.mapNotNull { it.toDoubleOrNull() }?.takeIf { it.size == 2 }
        if (wifi == null || home == null || last == null || wifi in known || last[2] > LEARN_ACCURACY) return known
        if (PlaceRule.meters(last[0], last[1], home[0], home[1]) > LEARN_METERS) return known
        val learned = (known + wifi).toList().takeLast(5).toSet()
        editor.putStringSet(KEY_HOME_WIFI, learned)
        return learned
    }

    /** The connected Wi-Fi's name, or null when not on Wi-Fi or Android hides it. */
    @Suppress("DEPRECATION")
    private fun wifiName(context: Context): String? = runCatching {
        val info = context.getSystemService(WifiManager::class.java)?.connectionInfo ?: return null
        info.ssid?.removeSurrounding("\"")?.takeIf { it.isNotBlank() && it != WifiManager.UNKNOWN_SSID && it != "<unknown ssid>" }
    }.getOrNull()

    private fun unreported(preferences: android.content.SharedPreferences): Set<String> =
        preferences.getStringSet(KEY_UNREPORTED, emptySet()).orEmpty()

    private fun parseSeen(text: String): PlaceRule.Seen? = text.split(",").takeIf { it.size == 2 }?.let { PlaceRule.Seen(it[0] == "1", it[1] == "1") }

    private fun seenText(seen: PlaceRule.Seen) = "${if (seen.wasIn) 1 else 0},${if (seen.wasOut) 1 else 0}"

    private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
