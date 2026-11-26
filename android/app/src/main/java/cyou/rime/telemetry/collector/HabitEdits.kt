package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/**
 * A change to the habits or the reasons made on the Habits screen, waiting to
 * reach the server. A habit added on the phone has a negative id until the
 * server gives it its own; later edits to it name that negative id and are
 * sent with the server's.
 */
data class HabitEdit(
    /** Tells two otherwise equal entries apart, so only the one sent is dropped. */
    val key: String,
    /** [ADD], [CHANGE] or [REASONS]. */
    val kind: String,
    /** The habit: a negative id for one added on the phone. Zero for [REASONS]. */
    val habitId: Long = 0,
    val name: String? = null,
    /** 0 Sunday to 6 Saturday; empty for every day; null when unchanged. */
    val weekdays: List<Int>? = null,
    val archived: Boolean? = null,
    /** The whole list, for [REASONS]. */
    val reasons: List<String>? = null,
) {
    companion object {
        const val ADD = "add"
        const val CHANGE = "change"
        const val REASONS = "reasons"
    }

    /** The call that sends it, as method, path and JSON body. */
    fun request(): Triple<String, String, String> = when (kind) {
        ADD -> Triple("POST", "/v1/device/habits", JSONObject().put("name", name).apply {
            weekdays?.let { put("weekdays", JSONArray(it)) }
        }.toString())
        REASONS -> Triple("PUT", "/v1/device/reasons", JSONObject().put("reasons", JSONArray(reasons.orEmpty())).toString())
        else -> Triple("PATCH", "/v1/device/habits/$habitId", JSONObject().apply {
            name?.let { put("name", it) }
            weekdays?.let { put("weekdays", JSONArray(it)) }
            archived?.let { put("archived", it) }
        }.toString())
    }
}

/** How sending one edit went. */
sealed interface EditSend {
    /** Accepted, with the server's answer. */
    data class Sent(val body: JSONObject?) : EditSend
    /** Refused for good (an empty name, say), with the server's words. */
    data class Refused(val message: String) : EditSend
    /** Not now: offline, or the server could not take it. Sent again later. */
    data object Retry : EditSend
}

/**
 * Edits saved on the phone first and sent in the order they were made, as
 * [TickQueue] does for ticks: an entry is dropped only once the server took
 * it or refused it for good, and one that has to wait keeps itself and every
 * entry after it. The store keeps the entries and which negative ids the
 * server has since given an id, so an edit made to a new habit while it was
 * being sent still reaches it.
 */
class EditQueue(private val store: TickStore) {
    data class Flushed(val sent: Int, val refusals: List<String>, val waiting: Int)

    private val sending = Any()

    private data class State(val entries: List<HabitEdit>, val resolved: Map<Long, Long>)

    @Synchronized
    fun entries(): List<HabitEdit> = load().entries

    /** Reads the entries and whatever else under the queue's lock, so a send never lands between the two. */
    @Synchronized
    fun <T> read(block: (List<HabitEdit>) -> T): T = block(load().entries)

    @Synchronized
    fun addHabit(name: String, weekdays: List<Int>? = null): HabitEdit {
        val state = load()
        // Below every negative id still in use, including those already given an id.
        val lowest = (state.entries.map { it.habitId } + state.resolved.keys).filter { it < 0 }.minOrNull() ?: 0
        val edit = HabitEdit(UUID.randomUUID().toString(), HabitEdit.ADD, lowest - 1, name = name, weekdays = weekdays)
        save(state.copy(entries = state.entries + edit))
        return edit
    }

    @Synchronized
    fun change(habitId: Long, name: String? = null, weekdays: List<Int>? = null, archived: Boolean? = null): HabitEdit {
        val state = load()
        val id = state.resolved[habitId] ?: habitId
        val edit = HabitEdit(UUID.randomUUID().toString(), HabitEdit.CHANGE, id, name = name, weekdays = weekdays, archived = archived)
        save(state.copy(entries = state.entries + edit))
        return edit
    }

    /** The whole list replaces any earlier list still waiting. */
    @Synchronized
    fun reasons(reasons: List<String>): HabitEdit {
        val state = load()
        val edit = HabitEdit(UUID.randomUUID().toString(), HabitEdit.REASONS, reasons = reasons)
        save(state.copy(entries = state.entries.filter { it.kind != HabitEdit.REASONS } + edit))
        return edit
    }

    /**
     * Sends the entries oldest first and stops at the first that has to wait.
     * [sent] runs for each accepted entry under the queue's lock, before the
     * entry is dropped, so what reads the queue with [read] sees one or the
     * other, never neither.
     */
    fun flush(send: (HabitEdit) -> EditSend, sent: (HabitEdit, JSONObject?) -> Unit = { _, _ -> }): Flushed = synchronized(sending) {
        var count = 0
        val refusals = mutableListOf<String>()
        while (true) {
            val edit = entries().firstOrNull() ?: break
            // A new habit the server refused: what came after for it can't reach it.
            if (edit.habitId < 0 && edit.kind != HabitEdit.ADD) {
                drop(edit.key)
                continue
            }
            when (val result = send(edit)) {
                EditSend.Retry -> break
                is EditSend.Sent -> synchronized(this) {
                    count++
                    sent(edit, result.body)
                    val id = result.body?.optJSONObject("habit")?.optLong("id", 0L) ?: 0L
                    if (edit.kind == HabitEdit.ADD && id > 0) resolve(edit.habitId, id)
                    drop(edit.key)
                }
                is EditSend.Refused -> {
                    refusals += result.message
                    drop(edit.key)
                }
            }
        }
        Flushed(count, refusals, entries().size)
    }

    @Synchronized
    private fun drop(key: String) {
        val state = load()
        save(state.copy(entries = state.entries.filter { it.key != key }))
    }

    @Synchronized
    private fun resolve(temporary: Long, id: Long) {
        val state = load()
        save(State(state.entries.map { if (it.habitId == temporary) it.copy(habitId = id) else it }, state.resolved + (temporary to id)))
    }

    private fun load(): State = decode(store.load())

    private fun save(state: State) = store.save(encode(state.entries, state.resolved))

    companion object {
        /** How the server's answer to an edit is taken. */
        fun outcome(answer: DeviceApi.Answer?): EditSend = when {
            answer == null -> EditSend.Retry
            answer.status in 200..299 -> EditSend.Sent(runCatching { JSONObject(answer.body) }.getOrNull())
            // Sending again can't change these: an empty or long name, a habit that is not there.
            answer.status == 400 || answer.status == 404 || answer.status == 409 ->
                EditSend.Refused(Budget.refusal(answer.body) ?: "The server refused this change (${answer.status}).")
            else -> EditSend.Retry
        }

        /**
         * The habits and reasons as the screen shows them: what the server last
         * gave with the waiting edits on top. Archived habits drop out.
         */
        fun apply(list: HabitList?, edits: List<HabitEdit>): HabitList? {
            if (edits.isEmpty()) return list
            val habits = (list?.habits.orEmpty()).toMutableList()
            var reasons = list?.reasons.orEmpty()
            for (edit in edits) when (edit.kind) {
                HabitEdit.ADD -> habits += HabitDef(edit.habitId, edit.name.orEmpty().trim(), (habits.maxOfOrNull { it.position } ?: -1) + 1,
                    edit.weekdays.orEmpty().toSet(), archived = false)
                HabitEdit.REASONS -> reasons = edit.reasons.orEmpty()
                else -> {
                    val index = habits.indexOfFirst { it.id == edit.habitId }
                    if (index >= 0) {
                        val habit = habits[index]
                        habits[index] = habit.copy(
                            name = edit.name?.trim() ?: habit.name,
                            weekdays = edit.weekdays?.toSet() ?: habit.weekdays,
                            archived = edit.archived ?: habit.archived,
                        )
                    }
                }
            }
            return HabitList(habits, reasons)
        }

        /** The weekdays to send: all seven or none is every day, sent as none. */
        fun normalize(days: Set<Int>): List<Int> {
            val valid = days.filter { it in 0..6 }.toSet()
            return if (valid.isEmpty() || valid.size == 7) emptyList() else valid.sorted()
        }

        private fun encode(entries: List<HabitEdit>, resolved: Map<Long, Long>): String = JSONObject()
            .put("entries", JSONArray().apply {
                for (edit in entries) put(JSONObject().apply {
                    put("key", edit.key).put("kind", edit.kind).put("habit_id", edit.habitId)
                    edit.name?.let { put("name", it) }
                    edit.weekdays?.let { put("weekdays", JSONArray(it)) }
                    edit.archived?.let { put("archived", it) }
                    edit.reasons?.let { put("reasons", JSONArray(it)) }
                })
            })
            .put("resolved", JSONObject().apply { for ((from, to) in resolved) put(from.toString(), to) })
            .toString()

        private fun decode(json: String?): State {
            val root = json?.let { runCatching { JSONObject(it) }.getOrNull() } ?: return State(emptyList(), emptyMap())
            val array = root.optJSONArray("entries") ?: JSONArray()
            val entries = (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                runCatching {
                    val kind = item.getString("kind").takeIf { it == HabitEdit.ADD || it == HabitEdit.CHANGE || it == HabitEdit.REASONS }!!
                    HabitEdit(
                        key = item.getString("key"),
                        kind = kind,
                        habitId = item.optLong("habit_id"),
                        name = if (item.has("name")) item.getString("name") else null,
                        weekdays = item.optJSONArray("weekdays")?.let { days -> (0 until days.length()).map { days.getInt(it) } },
                        archived = if (item.has("archived")) item.getBoolean("archived") else null,
                        reasons = item.optJSONArray("reasons")?.let { lines -> (0 until lines.length()).map { lines.getString(it) } },
                    )
                }.getOrNull()
            }
            val resolvedJson = root.optJSONObject("resolved")
            val resolved = resolvedJson?.keys()?.asSequence()?.mapNotNull { key ->
                key.toLongOrNull()?.let { it to resolvedJson.optLong(key) }
            }?.toMap().orEmpty()
            return State(entries, resolved)
        }
    }
}

/**
 * The phone's edit queue, kept in its own preferences and written at once, so
 * an edit survives the app being closed or the phone restarting. Sent by the
 * app after each edit and with every check, and by every collection cycle
 * ([PolicyWatch]), as ticks are ([HabitTicks]).
 */
object HabitEdits {
    private const val PREFS = "telemetry-habit-edits"
    private const val KEY_QUEUE = "queue"
    private const val KEY_NOTICE = "notice"

    @Volatile private var queue: EditQueue? = null

    fun queue(context: Context): EditQueue = queue ?: synchronized(this) {
        queue ?: EditQueue(object : TickStore {
            private val preferences = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            override fun load(): String? = preferences.getString(KEY_QUEUE, null)
            override fun save(json: String) {
                preferences.edit().putString(KEY_QUEUE, json).commit()
                HabitWidgets.changed(context)
            }
        }).also { queue = it }
    }

    /**
     * Sends what is waiting, in order. Each accepted edit goes into the saved
     * habit list at once, so the screen never shows it undone in between. A
     * refusal's message is kept to be shown once. Blocking IO.
     */
    fun flush(context: Context): EditQueue.Flushed {
        val flushed = queue(context).flush({ edit ->
            val (method, path, body) = edit.request()
            EditQueue.outcome(DeviceApi.send(context, method, path, body))
        }) { edit, body -> HabitCache.patch(context, edit, body) }
        flushed.refusals.lastOrNull()?.let { message ->
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_NOTICE, message).apply()
        }
        return flushed
    }

    /** The message of the last refused edit, once: reading it clears it. */
    fun takeNotice(context: Context): String? {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val message = preferences.getString(KEY_NOTICE, null) ?: return null
        preferences.edit().remove(KEY_NOTICE).apply()
        return message
    }
}
