package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** A clock window, "09:00"–"18:00". */
data class ClockWindow(val start: String, val end: String)

/** The budget's numbers (the policy's budget), in the panel's field order. */
data class BudgetNumbers(val enabled: Boolean, val values: Map<String, Double>) {
    operator fun get(key: String): Double = values[key] ?: 0.0

    fun toJson(): JSONObject = JSONObject().put("enabled", enabled).also { json ->
        for (field in BudgetFields.all) {
            val value = this[field.key]
            if (field.whole) json.put(field.key, Math.round(value)) else json.put(field.key, value)
        }
    }

    companion object {
        fun of(json: JSONObject?): BudgetNumbers? {
            json ?: return null
            return BudgetNumbers(json.optBoolean("enabled"), BudgetFields.all.associate { it.key to json.optDouble(it.key, 0.0).let { v -> if (v.isNaN()) 0.0 else v } })
        }
    }
}

data class BudgetField(val key: String, val label: String, val unit: String, val min: Double = 0.0, val max: Double, val whole: Boolean = true)

/** The panel's budget form (web/src/lib/BudgetRules.svelte), group by group. */
object BudgetFields {
    val groups: List<Pair<String, List<BudgetField>>> = listOf(
        "The day" to listOf(
            BudgetField("base_minutes", "Each day starts with", "min", max = 1440.0),
            BudgetField("max_minutes", "With habits, up to", "min", max = 1440.0),
            BudgetField("floor_minutes", "Never under", "min", max = 1440.0),
            BudgetField("habits_for_max", "Habits for the full bonus", "", min = 1.0, max = 50.0),
        ),
        "What a minute costs" to listOf(
            BudgetField("rate_distraction", "In distraction times", "×", max = 100.0, whole = false),
            BudgetField("rate_day", "In the daytime", "×", max = 100.0, whole = false),
            BudgetField("rate_bedtime", "In bedtime", "×", max = 100.0, whole = false),
            BudgetField("rate_grayscale_off", "Added while grayscale is kept off", "×", max = 100.0, whole = false),
            BudgetField("borrow_multiplier", "A minute borrowed costs tomorrow", "×", min = 1.0, max = 100.0, whole = false),
        ),
        "The door" to listOf(
            BudgetField("wait_minutes", "Wait before it opens", "min", max = 240.0),
            BudgetField("window_minutes", "Outside distraction times, a window lasts", "min", min = 1.0, max = 1440.0),
            BudgetField("window_use_minutes", "with use of", "min", max = 1440.0),
            BudgetField("grace_minutes", "and to close or save", "min", max = 60.0),
        ),
    )
    val all: List<BudgetField> = groups.flatMap { it.second }
}

data class DistractionGroup(val name: String, val apps: List<String>, val sites: List<String>)

/** The parts of /v1/policy/config the phone shows; [raw] is the whole config, kept as it came. */
data class PolicyView(
    val raw: JSONObject,
    val budget: BudgetNumbers?,
    val budgetNext: BudgetNumbers?,
    val budgetNextFrom: String?,
    val distractionTimes: List<ClockWindow>,
    val groups: List<DistractionGroup>,
    val closedByDefault: Boolean,
    val zone: ZoneId,
) {
    /** The budget in force at [now]: the waiting one once its time has come. */
    fun budgetInForce(now: Instant): BudgetNumbers? {
        val from = budgetNextFrom
        if (budgetNext != null && from != null && SettingsRules.started(from, now, zone)) return budgetNext
        return budget
    }
}

data class Bedtime(val enabled: Boolean, val start: String, val end: String, val windDownMinutes: Int) {
    fun toJson(): JSONObject = JSONObject().put("enabled", enabled).put("start", start).put("end", end).put("wind_down_minutes", windDownMinutes)
}

data class LockView(
    val enabled: Boolean,
    val windows: List<ClockWindow>,
    val pinWindows: List<ClockWindow>,
    val unlockMinutes: Int,
    val bedtime: Bedtime,
    val bedtimeNext: Bedtime?,
    val bedtimeNextFrom: String?,
    val locked: Boolean,
    val until: Instant?,
    val unlockedUntil: Instant?,
    val pinSet: Boolean,
    val pinOpen: Boolean,
    val triesLeft: Int,
    val extensionEnabled: Boolean,
    val extensionQuietSeconds: Int,
)

data class LabelView(val kind: String, val match: String, val label: String)

data class DeviceView(val id: String, val name: String, val revoked: Boolean)

data class SourceView(val kind: String, val device: String, val lastCollected: Instant?, val count: Long)

data class CollectionView(val checkedAt: Instant?, val devices: List<Pair<String, Instant?>>, val sources: List<SourceView>)

object SettingsRules {
    /** The earliest end, in minutes after midnight, an earlier bedtime may have and still apply at once. */
    private const val EARLIEST_WAKE = 5 * 60

    private fun objects(array: JSONArray?): List<JSONObject> =
        if (array == null) emptyList() else (0 until array.length()).mapNotNull { array.optJSONObject(it) }

    private fun strings(array: JSONArray?): List<String> =
        if (array == null) emptyList() else (0 until array.length()).map { array.optString(it).trim() }.filter { it.isNotEmpty() }

    private fun instant(text: String?): Instant? =
        text?.takeIf { it.isNotBlank() && it != "null" }?.let { runCatching { java.time.OffsetDateTime.parse(it).toInstant() }.getOrNull() }

    private fun windows(array: JSONArray?) = objects(array).map { ClockWindow(it.optString("start"), it.optString("end")) }

    fun policy(json: String): PolicyView? = runCatching {
        val root = JSONObject(json)
        val config = root.optJSONObject("policy")?.optJSONObject("config") ?: JSONObject()
        PolicyView(
            raw = config,
            budget = BudgetNumbers.of(config.optJSONObject("budget")),
            budgetNext = BudgetNumbers.of(config.optJSONObject("budget_next")),
            budgetNextFrom = config.optString("budget_next_from").ifBlank { null },
            distractionTimes = windows(config.optJSONArray("distraction_times")),
            groups = objects(config.optJSONArray("distractions")).map {
                DistractionGroup(it.optString("name").ifBlank { it.optString("id") }, strings(it.optJSONArray("android_packages")) + strings(it.optJSONArray("mac_apps")), strings(it.optJSONArray("domains")))
            },
            closedByDefault = config.optBoolean("closed_by_default"),
            zone = runCatching { ZoneId.of(config.optString("timezone")) }.getOrDefault(ZoneId.of("Europe/Istanbul")),
        )
    }.getOrNull()

    private fun bedtime(json: JSONObject?): Bedtime? = json?.let {
        Bedtime(it.optBoolean("enabled"), it.optString("start"), it.optString("end"), it.optInt("wind_down_minutes"))
    }

    fun lock(json: String): LockView? = runCatching {
        val root = JSONObject(json)
        val lock = root.getJSONObject("lock")
        val state = root.optJSONObject("state") ?: JSONObject()
        val extension = root.optJSONObject("extension") ?: JSONObject()
        LockView(
            enabled = lock.optBoolean("enabled"),
            windows = windows(lock.optJSONArray("windows")),
            pinWindows = windows(lock.optJSONArray("pin_windows")),
            unlockMinutes = lock.optInt("unlock_minutes", 15).takeIf { it > 0 } ?: 15,
            bedtime = bedtime(root.optJSONObject("bedtime")) ?: Bedtime(false, "", "", 0),
            bedtimeNext = bedtime(root.optJSONObject("bedtime_next")),
            bedtimeNextFrom = root.optString("bedtime_next_from").ifBlank { null },
            locked = state.optBoolean("locked"),
            until = instant(state.optString("until")),
            unlockedUntil = instant(state.optString("unlocked_until")),
            pinSet = state.optBoolean("pin_set"),
            pinOpen = state.optBoolean("pin_open"),
            triesLeft = state.optInt("tries_left"),
            extensionEnabled = extension.optBoolean("enabled"),
            extensionQuietSeconds = extension.optInt("quiet_seconds"),
        )
    }.getOrNull()

    fun labels(json: String): List<LabelView>? = runCatching {
        objects(JSONObject(json).optJSONArray("labels")).map { LabelView(it.optString("kind"), it.optString("match"), it.optString("label")) }
    }.getOrNull()

    fun devices(json: String): List<DeviceView>? = runCatching {
        objects(JSONObject(json).optJSONArray("devices")).map {
            DeviceView(it.optString("device_id"), it.optString("device_name"), !it.isNull("revoked_at") && it.optString("revoked_at").isNotBlank())
        }
    }.getOrNull()

    fun collection(json: String): CollectionView? = runCatching {
        val root = JSONObject(json)
        CollectionView(
            checkedAt = instant(root.optString("checked_at")),
            devices = objects(root.optJSONArray("devices")).map { it.optString("device_id") to instant(it.optString("last_received_at")) },
            sources = objects(root.optJSONArray("sources")).map {
                SourceView(it.optString("source_kind"), it.optString("device_id"), instant(it.optString("last_collected_at")), it.optLong("observation_count"))
            },
        )
    }.getOrNull()

    /** Whether a waiting rule's start has come: a moment, or a date as older rules waited for. */
    fun started(from: String, now: Instant, zone: ZoneId): Boolean {
        if (from.contains('T')) return instant(from)?.let { !it.isAfter(now) } ?: false
        return from <= now.atZone(zone).toLocalDate().toString()
    }

    fun formatWindows(windows: List<ClockWindow>): String = windows.joinToString(", ") { "${it.start}-${it.end}" }

    /** Budget minutes as the panel writes them: "1 h 40 min", "10 min". */
    fun budgetMinutes(minutes: Double): String = BudgetText.minutes(minutes)

    fun rate(value: Double): String = (if (value == Math.floor(value)) value.toLong().toString() else "%.2f".format(Locale.UK, value).trimEnd('0').trimEnd('.')) + "×"

    // The short current value on the right of each Settings row
    // (web/src/lib/settings.ts). Empty means unknown: the row shows nothing.

    fun budgetValue(policy: PolicyView?, now: Instant): String {
        policy ?: return ""
        val budget = policy.budgetInForce(now)
        if (budget?.enabled != true) return "Off"
        return "${budgetMinutes(budget["base_minutes"])} a day"
    }

    fun timesValue(policy: PolicyView?, lock: LockView?): String = listOfNotNull(
        formatWindows(policy?.distractionTimes.orEmpty()).ifEmpty { null },
        lock?.let { if (it.bedtime.enabled) "bed ${it.bedtime.start.ifBlank { "?" }}" else "no bedtime" },
    ).joinToString(" · ")

    fun distractionsValue(policy: PolicyView?, labels: List<LabelView>?): String {
        if (policy == null || labels == null) return ""
        val apps = mutableSetOf<String>()
        val sites = mutableSetOf<String>()
        fun site(value: String) = value.trim().lowercase().removePrefix("www.")
        for (group in policy.groups) {
            group.apps.forEach { apps += it.lowercase() }
            group.sites.forEach { if (site(it).isNotEmpty()) sites += site(it) }
        }
        for (label in labels) {
            if (label.label != "distraction" || label.match.isBlank()) continue
            if (label.kind == "site") sites += site(label.match) else apps += label.match.trim().lowercase()
        }
        val total = apps.size + sites.size
        return when {
            total == 0 -> "None"
            sites.isEmpty() -> "$total ${if (total == 1) "app" else "apps"}"
            apps.isEmpty() -> "$total ${if (total == 1) "site" else "sites"}"
            else -> "$total apps and sites"
        }
    }

    fun lockValue(lock: LockView?, zone: ZoneId): String {
        lock ?: return ""
        lock.unlockedUntil?.let { return "Open until ${BudgetText.clock(it, zone)}" }
        if (lock.locked) return lock.until?.let { "Locked until ${BudgetText.clock(it, zone)}" } ?: "Locked"
        return if (lock.enabled) "On" else "Off"
    }

    fun devicesValue(devices: List<DeviceView>?): String = devices?.let { "${it.count { device -> !device.revoked }} connected" }.orEmpty()

    fun sourcesValue(collection: CollectionView?): String = collection?.let { "${it.sources.size} ${if (it.sources.size == 1) "source" else "sources"}" }.orEmpty()

    /** The budget in one line, as the rules card shows it. */
    fun budgetSummary(b: BudgetNumbers?): String {
        if (b?.enabled != true) return "Off."
        val grace = if (b["grace_minutes"] > 0) " and ${b["grace_minutes"].toLong()} min to close" else ""
        return "On: ${budgetMinutes(b["base_minutes"])} a day, up to ${budgetMinutes(b["max_minutes"])} with ${b["habits_for_max"].toLong()} habits, never under ${budgetMinutes(b["floor_minutes"])}. " +
            "${rate(b["rate_distraction"])} in distraction times, ${rate(b["rate_day"])} in the daytime, ${rate(b["rate_bedtime"])} in bedtime, +${rate(b["rate_grayscale_off"])} with grayscale kept off. " +
            "Borrowing costs tomorrow ${rate(b["borrow_multiplier"])}. " +
            "A door waits ${b["wait_minutes"].toLong()} min, then gives ${b["window_use_minutes"].toLong()} min of use$grace in ${b["window_minutes"].toLong()} min."
    }

    /** Whether a budget asks less than the one in force, so it waits for the next day (the panel's budgetAsksLess). */
    fun budgetAsksLess(current: BudgetNumbers?, next: BudgetNumbers): Boolean {
        if (current?.enabled != true) return false
        if (!next.enabled) return true
        val higher = listOf("base_minutes", "max_minutes", "floor_minutes", "window_use_minutes", "grace_minutes").any { next[it] > current[it] }
        val lower = listOf("habits_for_max", "rate_distraction", "rate_day", "rate_bedtime", "rate_grayscale_off", "borrow_multiplier", "wait_minutes", "window_minutes").any { next[it] < current[it] }
        return higher || lower
    }

    private fun minutesOf(clock: String): Int? {
        val match = Regex("([01][0-9]|2[0-3]):([0-5][0-9])").matchEntire(clock) ?: return null
        return match.groupValues[1].toInt() * 60 + match.groupValues[2].toInt()
    }

    /** Hours a bedtime lasts, past midnight included. */
    fun bedtimeHours(start: String, end: String): Double {
        val from = minutesOf(start) ?: return 0.0
        val to = minutesOf(end) ?: return 0.0
        return ((to - from + 1440) % 1440) / 60.0
    }

    /**
     * Whether a bedtime asks less than the one in force: off, shorter at
     * either end, or a shorter wind-down. The server decides; this only
     * says so before it is sent.
     */
    fun bedtimeAsksLess(current: Bedtime, next: Bedtime): Boolean {
        if (!current.enabled) return false
        if (!next.enabled) return true
        val cs = minutesOf(current.start) ?: return true
        val ce = minutesOf(current.end) ?: return true
        val ns = minutesOf(next.start) ?: return true
        val ne = minutesOf(next.end) ?: return true
        if (next.windDownMinutes < current.windDownMinutes) return true
        val currentLength = (ce - cs + 1440) % 1440
        val nextLength = (ne - ns + 1440) % 1440
        // Moving bedtime earlier, no shorter and ending no earlier than
        // 05:00, asks no less (owner request 2026-11-27; the server's
        // Bedtime.movedEarlier).
        if (ne >= EARLIEST_WAKE && ne <= ce && nextLength >= currentLength) return false
        // Each minute of the current bedtime must still be inside the new one.
        val startsInside = (cs - ns + 1440) % 1440
        return startsInside + currentLength > nextLength
    }

    /** "23 Nov, 08:00" for a moment, "1 Nov" for a date. */
    fun waitStart(from: String?, zone: ZoneId): String {
        from ?: return ""
        if (!from.contains('T')) return runCatching { DateTimeFormatter.ofPattern("d MMM", Locale.UK).format(java.time.LocalDate.parse(from)) }.getOrDefault(from)
        val at = instant(from) ?: return from
        return DateTimeFormatter.ofPattern("d MMM, HH:mm", Locale.UK).format(at.atZone(zone))
    }
}

/**
 * A change to the owner's settings made on the phone and not yet accepted
 * by the server. It is kept on the phone until the server answers, in the
 * order made; a newer change of the same kind replaces a waiting one. The
 * server stays the authority: nothing here is shown as applied.
 */
data class SettingsChange(val id: Long, val kind: String, val payload: JSONObject, val label: String, val madeAt: Instant) {
    companion object {
        const val BUDGET = "budget"
        const val BEDTIME = "bedtime"
    }
}

/** What sending a change gave. */
sealed interface SendOutcome {
    /** Accepted; [notice] says when it applies. */
    data class Sent(val notice: String) : SendOutcome
    /** Refused; the server's own words. It is not sent again. */
    data class Refused(val message: String) : SendOutcome
    data object Unreachable : SendOutcome
}

class SettingsQueue(private val file: File, private val now: () -> Instant = Instant::now) {
    @Synchronized
    fun entries(): List<SettingsChange> = read()

    @Synchronized
    fun add(kind: String, payload: JSONObject, label: String): SettingsChange {
        val kept = read().filter { it.kind != kind }
        val id = (read().maxOfOrNull { it.id } ?: 0L) + 1
        val change = SettingsChange(id, kind, payload, label, now())
        write(kept + change)
        return change
    }

    /**
     * Sends the waiting changes in order. A change the server can't be
     * reached for stays, and the ones after it wait behind it. Returns what
     * the server said, change by change, for the screens to show.
     */
    fun flush(send: (SettingsChange) -> SendOutcome): List<Pair<SettingsChange, SendOutcome>> {
        val results = mutableListOf<Pair<SettingsChange, SendOutcome>>()
        while (true) {
            val next = synchronized(this) { read().firstOrNull() } ?: break
            val outcome = send(next)
            results += next to outcome
            if (outcome == SendOutcome.Unreachable) break
            synchronized(this) { write(read().filter { it.id != next.id }) }
        }
        return results
    }

    private fun read(): List<SettingsChange> {
        if (!file.isFile) return emptyList()
        val array = runCatching { JSONArray(file.readText()) }.getOrNull() ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            runCatching {
                SettingsChange(item.getLong("id"), item.getString("kind"), item.getJSONObject("payload"), item.optString("label"), Instant.parse(item.getString("made_at")))
            }.getOrNull()
        }
    }

    private fun write(changes: List<SettingsChange>) {
        file.parentFile?.mkdirs()
        val array = JSONArray(changes.map { JSONObject().put("id", it.id).put("kind", it.kind).put("payload", it.payload).put("label", it.label).put("made_at", it.madeAt.toString()) })
        val temp = File(file.path + ".new")
        temp.writeText(array.toString())
        if (!temp.renameTo(file)) {
            file.delete()
            temp.renameTo(file)
        }
    }
}
