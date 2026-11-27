package cyou.rime.telemetry.collector

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/** Activity's views, as the panel's bar names them. Plan is left out on the phone (owner 2026-11-27). */
enum class ActivitySection(val label: String) { TIMELINE("Timeline"), APPS("Apps"), HEALTH("Health"), PLACES("Places") }

/** How the last try to refresh an answer went. */
sealed interface Refresh {
    data object Never : Refresh
    data object Fresh : Refresh
    data object Unreachable : Refresh
    /** The server answered with a refusal; its own words. */
    data class Refused(val message: String) : Refresh
}

/** An answer as a screen shows it: the saved copy, when it came, and how the last refresh went. */
data class Shown<T>(val value: T?, val at: Instant?, val refresh: Refresh, val loading: Boolean = false)

/**
 * The server's answers for Activity, each kept on the phone and shown from
 * there at once; a fresh one is asked for quietly behind it. Lives as long
 * as the app's screen, so switching places never loads anything again.
 */
class ActivityData(private val context: Context, private val work: CoroutineScope, private val zone: ZoneId = ZoneId.systemDefault()) {
    var section by mutableStateOf(ActivitySection.TIMELINE)
    var day by mutableStateOf(LocalDate.now(zone).toString())

    /** Day, week, month or year, for all four views at once; kept while the app runs. */
    var scope by mutableStateOf(kept)
        private set

    companion object {
        @Volatile private var kept: Scope = Scope.DAY
    }

    val period: Period get() = Periods.of(scope, LocalDate.parse(day))

    val periods = PeriodData(context, work, zone)

    private val days = mutableStateMapOf<String, Shown<DayView>>()
    private val summaries = mutableStateMapOf<String, Shown<ActivitySummary>>()
    private val places = mutableStateMapOf<String, Shown<PlacesDay>>()
    private val healthDays = mutableStateMapOf<String, Shown<HealthDayView>>()
    private val loader = AnswerLoader(context, work)
    private val askedAt = mutableMapOf<String, Instant>()
    private val running = mutableSetOf<String>()

    fun timeline(day: String = this.day): Shown<DayView> = days[day] ?: Shown(null, null, Refresh.Never, loading = true)
    fun summary(day: String = this.day): Shown<ActivitySummary> = summaries[day] ?: Shown(null, null, Refresh.Never, loading = true)
    fun places(day: String = this.day): Shown<PlacesDay> = places[day] ?: Shown(null, null, Refresh.Never, loading = true)
    fun health(day: String = this.day): Shown<HealthDayView> = healthDays[day] ?: Shown(null, null, Refresh.Never, loading = true)

    val today: String get() = LocalDate.now(zone).toString()

    /** One day, week, month or year back or on ([by] -1 or 1), never into a period after today's. */
    fun move(by: Long) {
        val next = Periods.step(scope, LocalDate.parse(day), by, LocalDate.now(zone)) ?: return
        day = next.toString()
        show()
    }

    /** A sideways swipe of [dx] pixels: left to the next day or period, right to the one before. */
    fun swipe(dx: Float, threshold: Float) {
        when {
            dx <= -threshold -> move(1)
            dx >= threshold -> move(-1)
        }
    }

    fun goToday() {
        day = today
        show()
    }

    fun select(next: ActivitySection) {
        section = next
        show()
    }

    fun selectScope(next: Scope) {
        scope = next
        kept = next
        show()
    }

    /**
     * Shows what is saved for the place in view, then asks for it when it
     * was not asked lately; the days or periods either side are made ready
     * the same way, so a swipe shows its day at once.
     */
    fun show(force: Boolean = false) {
        val day = day
        if (scope != Scope.DAY) {
            showPeriod(force)
            return
        }
        load(section, day, force)
        for (near in DaySwipe.neighbours(day, today)) load(section, near, false)
    }

    private fun showPeriod(force: Boolean) {
        val today = LocalDate.now(zone)
        val period = period
        val near = listOf(-1L, 1L).mapNotNull { by -> Periods.step(scope, LocalDate.parse(day), by, today)?.let { Periods.of(scope, it) } }
        when (section) {
            ActivitySection.HEALTH -> {
                periods.loadHealth(period, today, force)
                if (scope != Scope.YEAR) near.forEach { periods.loadHealth(it, today) }
            }
            else -> {
                periods.loadRows(period, today, force)
                if (scope != Scope.YEAR) near.forEach { periods.loadRows(it, today) }
                // A week's map shows its positions, from each day's places.
                if (section == ActivitySection.PLACES && scope == Scope.WEEK) Periods.days(period, today).forEach { load(ActivitySection.PLACES, it.toString(), false) }
            }
        }
    }

    private fun load(section: ActivitySection, day: String, force: Boolean) {
        when (section) {
            ActivitySection.TIMELINE -> loader.load("day-$day", force, { DayModel.parse(it) }, { days[day] = it }, { days[day] }) {
                DeviceApi.send(context, "GET", "/v1/device/day?date=$day")?.let { RawAnswer(it.status, "application/json", it.body.toByteArray()) }
            }
            ActivitySection.APPS -> loader.load("summary-$day", force, { SummaryModel.parse(it) }, { summaries[day] = it }, { summaries[day] }) {
                OwnerApi.get(context, "/v1/activity/summary?date=$day")
            }
            // Without the server, the phone's own positions, once the server could not answer.
            ActivitySection.PLACES -> loader.load("places-$day", force, { placesFor(day, it) }, { places[day] = it }, { places[day] },
                fallback = { placesFor(day, null)?.takeIf { it.positions.isNotEmpty() } }) {
                OwnerApi.get(context, "/v1/places/day?date=$day")
            }
            ActivitySection.HEALTH -> showHealth(day, force)
        }
    }

    /** The day's places: the server's answer, or none, with this phone's own positions added. */
    private fun placesFor(day: String, json: String?): PlacesDay? {
        val date = LocalDate.parse(day)
        val from = date.atStartOfDay(zone).toInstant()
        val end = date.plusDays(1).atStartOfDay(zone).toInstant()
        val to = minOf(end, Instant.now())
        val server = json?.let(Places::parse)
        if (json != null && server == null) return null
        val local = runCatching { FixLog.of(context).between(from, to, zone) }.getOrDefault(emptyList())
        return Places.merge(server, day, zone, from, to, local, Places.rulesFrom(HabitCache.list(context)))
    }

    /** One day of health from Health Connect on the phone: the saved read at once, a fresh one behind it. */
    private fun showHealth(day: String, force: Boolean) {
        val key = HealthDays.key(day)
        if (key in running) return
        val asked = askedAt[key]
        if (!force && asked != null && Duration.between(asked, Instant.now()) < AnswerLoader.EVERY) return
        running += key
        askedAt[key] = Instant.now()
        work.launch {
            val known = healthDays[day]
            if (known?.value == null) {
                val saved = withContext(Dispatchers.IO) { HealthDays.saved(context, day) }
                healthDays[day] = Shown(saved?.first, saved?.second, known?.refresh ?: Refresh.Never, loading = true)
            } else healthDays[day] = known.copy(loading = true)
            val fresh = withContext(Dispatchers.IO) { runCatching { HealthDays.read(context, LocalDate.parse(day), zone) }.getOrNull() }
            val before = healthDays[day]
            healthDays[day] = if (fresh != null) Shown(fresh, Instant.now(), Refresh.Fresh)
            else Shown(before?.value, before?.at, Refresh.Refused("Health Connect can't be read on this phone."))
            running -= key
        }
    }

    /** Drops per-day answers older than three weeks. */
    fun prune() {
        work.launch(Dispatchers.IO) { runCatching { AnswerStore.of(context).prune(LocalDate.now(zone)) } }
    }
}

/**
 * Shows a saved answer at once and asks the server for a fresh one behind
 * it, at most once a minute per answer while it is on the screen. Only an
 * answer that reads replaces the saved copy.
 */
class AnswerLoader(private val context: Context, private val scope: CoroutineScope) {
    companion object {
        val EVERY: Duration = Duration.ofMinutes(1)
    }

    private val askedAt = mutableMapOf<String, Instant>()
    private val running = mutableSetOf<String>()

    fun <T> load(
        key: String,
        force: Boolean,
        parse: (String) -> T?,
        set: (Shown<T>) -> Unit,
        get: () -> Shown<T>?,
        /** What to show when the try failed and nothing is saved, such as what the phone has itself. */
        fallback: (() -> T?)? = null,
        fetch: () -> RawAnswer?,
    ) {
        if (key in running) return
        val asked = askedAt[key]
        val now = Instant.now()
        val known = get()
        if (!force && known != null && asked != null && Duration.between(asked, now) < EVERY && known.refresh != Refresh.Unreachable) return
        running += key
        askedAt[key] = now
        scope.launch {
            // The saved copy first, so the place shows at once; nothing is shown as the answer before it comes.
            if (known?.value == null) {
                val saved = withContext(Dispatchers.IO) {
                    AnswerStore.of(context).read(key)?.let { entry -> parse(entry.json)?.let { it to entry.at } }
                }
                set(Shown(saved?.first, saved?.second, known?.refresh ?: Refresh.Never, loading = true))
            } else set(known.copy(loading = true))
            val (value, refresh) = withContext(Dispatchers.IO) {
                val answer = runCatching(fetch).getOrNull()
                when {
                    answer == null -> null to Refresh.Unreachable
                    !answer.ok -> null to Refresh.Refused(answer.message())
                    else -> {
                        val parsed = parse(answer.text)
                        if (parsed != null) {
                            AnswerStore.of(context).put(key, answer.text)
                            parsed to Refresh.Fresh
                        } else null to Refresh.Refused("The server's answer couldn't be read.")
                    }
                }
            }
            val before = get()
            set(
                when {
                    value != null -> Shown(value, Instant.now(), refresh)
                    before?.value != null || fallback == null -> Shown(before?.value, before?.at, refresh)
                    else -> Shown(withContext(Dispatchers.IO) { runCatching(fallback).getOrNull() }, null, refresh)
                },
            )
            running -= key
        }
    }

}
