package cyou.rime.telemetry.collector

import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import kotlin.math.ceil

/**
 * One sitting on distractions: the time they were in front, across switches
 * between them. Leaving for longer than [gap] starts a new sitting.
 */
class Sitting(private val gap: Duration = Duration.ofMinutes(2)) {
    /** The stretches in front in this sitting; the last one is still open while [inFront]. */
    private val stretches = mutableListOf<Pair<Instant, Instant?>>()

    val inFront: Boolean get() = stretches.lastOrNull()?.second == null && stretches.isNotEmpty()

    fun enter(now: Instant) {
        if (inFront) return
        val leftAt = stretches.lastOrNull()?.second
        if (leftAt != null && Duration.between(leftAt, now) > gap) stretches.clear()
        stretches += now to null
    }

    fun leave(now: Instant) {
        if (!inFront) return
        val start = stretches.removeAt(stretches.size - 1).first
        stretches += start to now
    }

    /** Time in front this sitting. */
    fun used(now: Instant): Duration = usedSince(Instant.MIN, now)

    /** Time in front this sitting since [since], such as since the budget was last fetched. */
    fun usedSince(since: Instant, now: Instant): Duration = stretches.fold(Duration.ZERO) { total, (start, end) ->
        val from = if (start.isAfter(since)) start else since
        val to = end ?: now
        if (to.isAfter(from)) total + Duration.between(from, to) else total
    }
}

/**
 * What the corner counter says: the time this sitting and the rate, then the
 * window's use, the grace or the budget left, and the bedtime during the
 * wind-down. The budget moves on between fetches by what this phone measured
 * in front since then, so it never shows more than is left.
 */
object CounterText {
    fun lines(
        budget: BudgetView,
        sitting: Duration,
        usedSinceFetch: Duration,
        now: Instant,
        zone: ZoneId,
    ): List<String> {
        val local = usedSinceFetch.seconds / 60.0
        val lines = mutableListOf("${BudgetText.minutes(sitting.seconds / 60.0)} · ${BudgetText.number(budget.rate.total)}×")
        val door = budget.door?.takeIf { it.open }
        val use = door?.useMinutes
        when {
            door != null && door.state == "grace" -> lines += grace(door, now)
            door != null && use != null -> {
                val used = (door.usedMinutes + local).coerceAtMost(use)
                lines += "${BudgetText.number(Math.floor(used))} of ${BudgetText.number(use)} min"
            }
            else -> {
                val left = budget.left - local * budget.rate.total
                lines += if (left > 0) "${BudgetText.minutes(left)} left" else "Borrowing from tomorrow"
            }
        }
        BudgetText.bedtime(budget, now, null, zone)?.let { lines += it }
        return lines
    }

    /** "1 min to close or save", until the door's sliding end. */
    fun grace(door: BudgetDoor, now: Instant): String {
        val end = door.endsAt ?: return "1 min to close or save"
        val minutes = ceil(Duration.between(now, end).seconds / 60.0).coerceAtLeast(1.0)
        return "${BudgetText.number(minutes)} min to close or save"
    }
}
