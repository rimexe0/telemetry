package cyou.rime.telemetry.collector

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/**
 * Activity's weeks, months and years: each day's row from the server and
 * each day's health from Health Connect, kept on the phone a month to a
 * file ("rows-2026-11", "healthrows-2026-11", never dropped with the
 * per-day answers), so a period opens at once once its days are kept. Only
 * the days that may still change are asked for again: today, days the
 * server has not called final, days counted with other labels, and days
 * never kept.
 */
class PeriodData(private val context: Context, private val scope: CoroutineScope, private val zone: ZoneId) {
    private val rows = mutableStateMapOf<String, DayRow>()
    private val health = mutableStateMapOf<String, HealthDay>()
    private var healthMissing by mutableStateOf<List<String>>(emptyList())
    /** The habits' places, for naming stays and drawing their circles. */
    var rules by mutableStateOf<List<HabitPlace>>(emptyList())
        private set

    /** Months read from the phone's store, with when each was kept. */
    private val months = mutableStateMapOf<String, Instant?>()
    private val raw = mutableMapOf<String, JSONObject>()
    private val status = mutableStateMapOf<String, Shown<Unit>>()
    private val askedAt = mutableMapOf<String, Instant>()
    private val running = mutableSetOf<String>()
    private val disk = Mutex()
    /** The labels the server last counted with; days counted with others are asked for again. */
    private var version: String? = null

    private fun monthsOf(period: Period, today: LocalDate): List<String> =
        Periods.days(period, today).map { it.toString().substring(0, 7) }.distinct()

    /**
     * [period] as [make] builds it from the kept days, once the period's
     * months are read and every day is there or the last try is over;
     * until then nothing, so the page shows its grey shapes.
     */
    private fun <T> shown(prefix: String, period: Period, today: LocalDate, has: (LocalDate) -> Boolean, make: (List<LocalDate>) -> T): Shown<T> {
        val keys = monthsOf(period, today).map { "$prefix-$it" }
        val state = status["$prefix-${period.key}"] ?: Shown(Unit, null, Refresh.Never, loading = true)
        if (keys.any { it !in months }) return Shown(null, null, Refresh.Never, loading = true)
        val days = Periods.days(period, today)
        val at = keys.mapNotNull { months[it] }.maxOrNull()
        val complete = days.all(has)
        // Kept days show at once while the rest are asked for (owner 2026-11-27): the
        // grey shapes only while nothing of the period is kept yet.
        if (!complete && state.loading && days.none(has)) return Shown(null, at, Refresh.Never, loading = true)
        if (!complete && days.none(has) && state.refresh != Refresh.Fresh) return Shown(null, at, state.refresh)
        return Shown(make(days), state.at ?: at, state.refresh, state.loading)
    }

    fun activity(period: Period, today: LocalDate): Shown<PeriodActivity> =
        shown("rows", period, today, { it.toString() in rows }) { PeriodModel.activity(it, rows) }

    fun places(period: Period, today: LocalDate): Shown<PeriodPlaces> =
        shown("rows", period, today, { it.toString() in rows }) { PeriodModel.places(it, rows, rules) }

    /** The kept rows of [period]'s days, for the map's stays. */
    fun rowsOf(period: Period, today: LocalDate): List<DayRow> = Periods.days(period, today).mapNotNull { rows[it.toString()] }

    fun health(period: Period, today: LocalDate): Shown<PeriodHealth> =
        shown("healthrows", period, today, { it.toString() in health }) { PeriodModel.health(it, health, healthMissing) }

    private suspend fun readMonths(prefix: String, period: Period, today: LocalDate) {
        for (month in monthsOf(period, today)) {
            val key = "$prefix-$month"
            if (key in months) continue
            val (entry, json) = withContext(Dispatchers.IO) {
                disk.withLock {
                    val entry = runCatching { AnswerStore.of(context).read(key) }.getOrNull()
                    val json = entry?.json?.let { runCatching { JSONObject(it) }.getOrNull() } ?: JSONObject()
                    raw[key] = json
                    entry to JSONObject(json.toString())
                }
            }
            for (date in json.keys()) {
                val item = json.optJSONObject(date) ?: continue
                if (prefix == "rows") PeriodModel.row(item)?.let { rows[it.date] = it }
                else HealthDays.day(item)?.let { health[it.date] = it }
            }
            months[key] = entry?.at
        }
    }

    private suspend fun keep(prefix: String, items: List<Pair<String, JSONObject>>) {
        val now = Instant.now()
        withContext(Dispatchers.IO) {
            disk.withLock {
                for ((month, group) in items.groupBy { it.first.substring(0, 7) }) {
                    val key = "$prefix-$month"
                    val json = raw.getOrPut(key) { JSONObject() }
                    group.forEach { (date, item) -> json.put(date, item) }
                    runCatching { AnswerStore.of(context).put(key, json.toString(), now) }
                }
            }
        }
        items.map { "$prefix-${it.first.substring(0, 7)}" }.distinct().forEach { months[it] = now }
    }

    private fun begin(key: String, force: Boolean): Boolean {
        if (key in running) return false
        val asked = askedAt[key]
        val state = status[key]
        if (!force && asked != null && Duration.between(asked, Instant.now()) < AnswerLoader.EVERY && state?.refresh != Refresh.Unreachable) return false
        running += key
        askedAt[key] = Instant.now()
        status[key] = (state ?: Shown(Unit, null, Refresh.Never)).copy(loading = true)
        return true
    }

    /** Shows what is kept for [period]'s days at once, then asks the server for the days that may still change. */
    fun loadRows(period: Period, today: LocalDate, force: Boolean = false) {
        val key = "rows-${period.key}"
        if (!begin(key, force)) return
        scope.launch {
            if (version == null) version = withContext(Dispatchers.IO) { runCatching { AnswerStore.of(context).read("rows-version")?.json }.getOrNull() }
            rules = withContext(Dispatchers.IO) { runCatching { Places.rulesFrom(HabitCache.list(context)) }.getOrDefault(emptyList()) }
            readMonths("rows", period, today)
            val days = Periods.days(period, today)
            var refresh: Refresh = Refresh.Fresh
            var asked = false
            // Twice at most: a first answer counted with new labels makes the kept days stale.
            for (round in 0 until 2) {
                val before = version
                // Nothing to ask: the last day still says whether the labels changed.
                val stale = PeriodModel.stale(days, rows, version).ifEmpty { if (asked) emptyList() else listOf(days.last()) }
                for ((from, to) in PeriodModel.ranges(stale)) {
                    asked = true
                    // Up to 62 days the server may still be working out the first time.
                    val answer = withContext(Dispatchers.IO) { runCatching { OwnerApi.send(context, "GET", "/v1/activity/days?from=$from&to=$to", readTimeoutMillis = 90_000) }.getOrNull() }
                    refresh = when {
                        answer == null -> Refresh.Unreachable
                        !answer.ok -> Refresh.Refused(answer.message())
                        else -> if (take(answer.text)) Refresh.Fresh else Refresh.Refused("The server's answer couldn't be read.")
                    }
                    if (refresh != Refresh.Fresh) break
                }
                if (refresh != Refresh.Fresh || version == before) break
            }
            status[key] = Shown(Unit, if (refresh == Refresh.Fresh) Instant.now() else status[key]?.at, refresh)
            running -= key
        }
    }

    /** Keeps the rows of one answer; false when it doesn't read. */
    private suspend fun take(text: String): Boolean {
        val root = runCatching { JSONObject(text) }.getOrNull() ?: return false
        val version = root.optString("version").ifBlank { return false }
        val days = root.optJSONArray("days") ?: return false
        val kept = mutableListOf<Pair<String, JSONObject>>()
        for (i in 0 until days.length()) {
            val item = days.optJSONObject(i) ?: continue
            val saved = PeriodModel.keep(item, version)
            val row = PeriodModel.row(saved) ?: continue
            rows[row.date] = row
            kept += row.date to saved
        }
        keep("rows", kept)
        if (version != this.version) {
            this.version = version
            withContext(Dispatchers.IO) { runCatching { AnswerStore.of(context).put("rows-version", version) } }
        }
        return true
    }

    /** Health Connect for [period]'s days: kept days at once, then today, the two days before and days never read. */
    fun loadHealth(period: Period, today: LocalDate, force: Boolean = false) {
        val key = "healthrows-${period.key}"
        if (!begin(key, force)) return
        scope.launch {
            readMonths("healthrows", period, today)
            val recent = today.minusDays(2)
            val stale = Periods.days(period, today).filter { it.toString() !in health || !it.isBefore(recent) }
            var refresh: Refresh = Refresh.Fresh
            for ((from, to) in PeriodModel.ranges(stale, most = 400)) {
                val read = withContext(Dispatchers.IO) { runCatching { HealthDays.readDays(context, from, to, zone) }.getOrNull() }
                if (read == null) {
                    refresh = Refresh.Refused("Health Connect can't be read on this phone.")
                    break
                }
                healthMissing = read.second
                read.first.forEach { health[it.date] = it }
                keep("healthrows", read.first.map { it.date to HealthDays.dayJson(it) })
            }
            status[key] = Shown(Unit, if (refresh == Refresh.Fresh) Instant.now() else status[key]?.at, refresh)
            running -= key
        }
    }
}
