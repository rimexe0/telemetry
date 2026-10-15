package cyou.rime.telemetry.wear

import java.time.Duration
import java.time.Instant
import org.json.JSONArray
import org.json.JSONObject

/** One position on a walk, and whether the watch was on the wrist then. */
data class WalkPoint(val at: Instant, val lat: Double, val lon: Double, val accuracy: Float, val worn: Boolean, val bpm: Int?)

/**
 * A walk the watch records while it cannot reach the phone: its positions,
 * each marked worn or not, its steps, and why it started. The server decides
 * what of it counts as outside; the watch only records honestly. Without
 * Android so it can be tested.
 */
class WalkLog(val id: String, val startedAt: Instant, val trigger: String) {
    private val points = mutableListOf<WalkPoint>()
    var steps: Long = 0
        private set
    var endedAt: Instant? = null
        private set

    val size: Int get() = points.size

    fun add(point: WalkPoint) {
        // GPS can repeat a fix; one per instant is enough.
        if (points.lastOrNull()?.at == point.at) return
        points += point
    }

    fun addSteps(count: Long) {
        if (count > 0) steps += count
    }

    fun end(at: Instant) {
        endedAt = at
    }

    /** Minutes the watch was worn with a position, roughly, for the watch face. */
    fun wornMinutes(): Long {
        var total = Duration.ZERO
        for (i in 1 until points.size) {
            val a = points[i - 1]
            val b = points[i]
            val gap = Duration.between(a.at, b.at)
            if (a.worn && b.worn && gap <= Duration.ofMinutes(5)) total = total.plus(gap)
        }
        return total.toMinutes()
    }

    fun toJson(): JSONObject {
        val array = JSONArray()
        for (point in thin(points, MAX_POINTS)) {
            array.put(JSONObject().put("t", point.at.toString()).put("lat", point.lat).put("lon", point.lon)
                .put("acc", point.accuracy.toDouble()).put("worn", point.worn).apply { point.bpm?.let { put("bpm", it) } })
        }
        return JSONObject().put("session_id", id).put("start", startedAt.toString()).put("end", (endedAt ?: startedAt).toString())
            .put("trigger", trigger).put("steps", steps).put("points", array)
    }

    companion object {
        /** Keeps a walk's message well under the watch link's 100 KB limit. */
        const val MAX_POINTS = 900

        fun <T> thin(points: List<T>, max: Int): List<T> {
            if (points.size <= max) return points
            val step = (points.size + max - 1) / max
            val kept = points.filterIndexed { index, _ -> index % step == 0 }
            return if (kept.last() == points.last()) kept else kept + points.last()
        }
    }
}

/**
 * GPS only runs while walking: the link to the phone also drops in a far
 * room of the house, and sitting there should not drain the watch. It pauses
 * after a stretch without steps and resumes on the next steps.
 */
object GpsPacing {
    val PAUSE_AFTER: Duration = Duration.ofMinutes(10)
    const val RESUME_STEPS = 20L

    fun shouldRun(lastStepsAt: Instant, now: Instant): Boolean = Duration.between(lastStepsAt, now) < PAUSE_AFTER
}

/**
 * Whether the watch is on the wrist: the off-body sensor when the watch has
 * one, else a heart rate in the last 90 seconds.
 */
object Wearing {
    val HEART_RATE_FRESH: Duration = Duration.ofSeconds(90)

    fun worn(onBody: Boolean?, lastHeartRateAt: Instant?, now: Instant): Boolean = when {
        onBody != null -> onBody
        lastHeartRateAt != null -> Duration.between(lastHeartRateAt, now) <= HEART_RATE_FRESH
        else -> false
    }
}
