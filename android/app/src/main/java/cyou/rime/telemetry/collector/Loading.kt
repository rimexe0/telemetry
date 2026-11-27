package cyou.rime.telemetry.collector

import java.time.LocalDate

/**
 * What a place on the screen draws while an answer is on its way, so
 * waiting is never shown as data: "No positions this day" only once the
 * day's answer has come.
 */
sealed interface Showing<out T> {
    /** Nothing saved for it yet and nothing else to show: grey shapes in the real layout. */
    data object Placeholder : Showing<Nothing>

    /** Another day's answer, dimmed, while this one comes. */
    data class Dimmed<T>(val value: T) : Showing<T>

    /** The answer, saved or fresh. */
    data class Ready<T>(val value: T) : Showing<T>

    /** The try failed and nothing is saved: one muted line. */
    data object Failed : Showing<Nothing>
}

object LoadState {
    /**
     * [shown] as it should be drawn: its value once there is one; the
     * failure once the try failed; else [previous] (what was on the screen
     * before, another day's) dimmed, or the placeholder.
     */
    fun <T> of(shown: Shown<T>, previous: T?): Showing<T> {
        val value = shown.value
        return when {
            value != null -> Showing.Ready(value)
            shown.refresh is Refresh.Unreachable || shown.refresh is Refresh.Refused -> Showing.Failed
            previous != null -> Showing.Dimmed(previous)
            else -> Showing.Placeholder
        }
    }
}

/** A sideways swipe on a dated page: left goes to the next day, right to the day before, never past today. */
object DaySwipe {
    /** The day a swipe of [dx] pixels leads to, or null when it is too short or would go past [today]. */
    fun target(day: String, today: String, dx: Float, threshold: Float): String? {
        val date = runCatching { LocalDate.parse(day) }.getOrNull() ?: return null
        val last = runCatching { LocalDate.parse(today) }.getOrNull() ?: return null
        val next = when {
            dx <= -threshold -> date.plusDays(1)
            dx >= threshold -> date.minusDays(1)
            else -> return null
        }
        return if (next.isAfter(last)) null else next.toString()
    }

    /** The days to have ready around [day]: the day before, and the day after when it is not in the future. */
    fun neighbours(day: String, today: String): List<String> {
        val date = runCatching { LocalDate.parse(day) }.getOrNull() ?: return emptyList()
        val last = runCatching { LocalDate.parse(today) }.getOrNull() ?: return emptyList()
        return listOfNotNull(date.minusDays(1), date.plusDays(1).takeIf { !it.isAfter(last) }).map { it.toString() }
    }
}
