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
class ActivityData(private val context: Context, private val scope: CoroutineScope, private val zone: ZoneId = ZoneId.systemDefault()) {
    var section by mutableStateOf(ActivitySection.TIMELINE)
    var day by mutableStateOf(LocalDate.now(zone).toString())

    private val days = mutableStateMapOf<String, Shown<DayView>>()
    private val summaries = mutableStateMapOf<String, Shown<ActivitySummary>>()
    private val places = mutableStateMapOf<String, Shown<PlacesDay>>()
    var health by mutableStateOf(Shown<HealthWeek>(null, null, Refresh.Never))
        private set
    private val loader = AnswerLoader(context, scope)
    private val askedAt = mutableMapOf<String, Instant>()
    private val running = mutableSetOf<String>()

    fun timeline(day: String = this.day): Shown<DayView> = days[day] ?: Shown(null, null, Refresh.Never, loading = true)
    fun summary(day: String = this.day): Shown<ActivitySummary> = summaries[day] ?: Shown(null, null, Refresh.Never, loading = true)
    fun places(day: String = this.day): Shown<PlacesDay> = places[day] ?: Shown(null, null, Refresh.Never, loading = true)

    val today: String get() = LocalDate.now(zone).toString()

    fun move(days: Long) {
        val next = LocalDate.parse(day).plusDays(days)
        if (next.isAfter(LocalDate.now(zone))) return
        day = next.toString()
        show()
    }

    fun goToday() {
        day = today
        show()
    }

    fun select(next: ActivitySection) {
        section = next
        show()
    }

    /** Shows what is saved for the place in view, then asks the server when it was not asked lately. */
    fun show(force: Boolean = false) {
        val day = day
        when (section) {
            ActivitySection.TIMELINE -> loader.load("day-$day", force, { DayModel.parse(it) }, { days[day] = it }, { days[day] }) {
                DeviceApi.send(context, "GET", "/v1/device/day?date=$day")?.let { RawAnswer(it.status, "application/json", it.body.toByteArray()) }
            }
            ActivitySection.APPS -> loader.load("summary-$day", force, { SummaryModel.parse(it) }, { summaries[day] = it }, { summaries[day] }) {
                OwnerApi.get(context, "/v1/activity/summary?date=$day")
            }
            ActivitySection.PLACES -> loader.load("places-$day", force, { placesFor(day, it) }, { places[day] = it }, { places[day] }, empty = { placesFor(day, null) }) {
                OwnerApi.get(context, "/v1/places/day?date=$day")
            }
            ActivitySection.HEALTH -> showHealth(force)
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

    private fun showHealth(force: Boolean) {
        val key = "health"
        if (key in running) return
        val asked = askedAt[key]
        if (!force && asked != null && Duration.between(asked, Instant.now()) < AnswerLoader.EVERY) return
        running += key
        askedAt[key] = Instant.now()
        scope.launch {
            if (health.value == null) {
                val saved = withContext(Dispatchers.IO) { HealthDays.saved(context) }
                health = Shown(saved?.first, saved?.second, Refresh.Never, loading = true)
            }
            val fresh = runCatching { HealthDays.refresh(context, LocalDate.now(zone), zone) }.getOrNull()
            health = if (fresh != null) Shown(fresh, Instant.now(), Refresh.Fresh)
            else health.copy(loading = false, refresh = Refresh.Refused("Health Connect can't be read on this phone."))
            running -= key
        }
    }

    /** Drops per-day answers older than three weeks. */
    fun prune() {
        scope.launch(Dispatchers.IO) { runCatching { AnswerStore.of(context).prune(LocalDate.now(zone)) } }
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
        empty: (() -> T?)? = null,
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
            // The saved copy first, so the place shows at once.
            if (known?.value == null) {
                val saved = withContext(Dispatchers.IO) {
                    AnswerStore.of(context).read(key)?.let { entry -> parse(entry.json)?.let { it to entry.at } }
                }
                val emptyValue = if (saved == null && empty != null) withContext(Dispatchers.IO) { empty() } else null
                set(Shown(saved?.first ?: emptyValue, saved?.second, known?.refresh ?: Refresh.Never, loading = true))
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
                if (value != null) Shown(value, Instant.now(), refresh)
                else Shown(before?.value, before?.at, refresh),
            )
            running -= key
        }
    }

}
