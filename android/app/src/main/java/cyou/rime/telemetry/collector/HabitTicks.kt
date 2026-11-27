package cyou.rime.telemetry.collector

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.util.UUID

/** A tick or untick made on the phone, waiting to reach the server. */
data class HabitTick(
    /** Tells two otherwise equal entries apart, so only the one sent is dropped. */
    val key: String,
    val habitId: Long,
    /** The budget day it is for. */
    val day: String,
    /** When it was tapped. */
    val at: Instant,
    /** "tick" or "untick". */
    val action: String,
    /** Made by a habit rule, not by a tap: the server keeps it only while nobody ticked or unticked the habit that day. */
    val byRule: Boolean = false,
) {
    val done: Boolean get() = action == TICK

    companion object {
        const val TICK = "tick"
        const val UNTICK = "untick"
    }
}

/** How sending one entry went. */
sealed interface TickSend {
    /** Accepted; [budget] is the server's new budget, when it gave one. */
    data class Sent(val budget: JSONObject?) : TickSend
    /** Refused for good (the day closed, say), with the server's words. */
    data class Refused(val message: String) : TickSend
    /** Not now: offline, or the server could not take it. Sent again later. */
    data object Retry : TickSend
}

/** Where the queue is kept. */
interface TickStore {
    fun load(): String?
    fun save(json: String)
}

/**
 * Ticks saved on the phone first and sent in the order they were made, so a
 * tick works while the server is down and an untick never overtakes its
 * tick. An entry is dropped only once the server took it or refused it for
 * good; anything else keeps it, and every entry after it, for the next try.
 */
class TickQueue(private val store: TickStore) {
    data class Flushed(val sent: Int, val refusals: List<String>, val budget: JSONObject?, val waiting: Int)

    /** Held while sending, apart from the store's own lock, so a tap never waits on the network. */
    private val sending = Any()

    @Synchronized
    fun entries(): List<HabitTick> = decode(store.load())

    @Synchronized
    fun add(habitId: Long, day: String, at: Instant, done: Boolean, byRule: Boolean = false): HabitTick {
        val tick = HabitTick(UUID.randomUUID().toString(), habitId, day, at, if (done) HabitTick.TICK else HabitTick.UNTICK, byRule = byRule && done)
        store.save(encode(entries() + tick))
        return tick
    }

    /**
     * Sends the entries oldest first and stops at the first that has to wait.
     * Only one flush runs at a time, so no entry is sent twice at once.
     */
    fun flush(send: (HabitTick) -> TickSend): Flushed = synchronized(sending) {
        var sent = 0
        var budget: JSONObject? = null
        val refusals = mutableListOf<String>()
        for (tick in entries()) {
            when (val result = send(tick)) {
                TickSend.Retry -> break
                is TickSend.Sent -> {
                    sent++
                    result.budget?.let { budget = it }
                }
                // A rule's tick refused (the day closed, say) is not the owner's to hear about.
                is TickSend.Refused -> if (!tick.byRule) refusals += result.message
            }
            remove(tick.key)
        }
        Flushed(sent, refusals, budget, entries().size)
    }

    @Synchronized
    private fun remove(key: String) = store.save(encode(entries().filter { it.key != key }))

    companion object {
        /** What the queued entries for [day] leave each habit at: the last one wins. */
        fun doneByQueue(entries: List<HabitTick>, day: String): Map<Long, Boolean> {
            val result = mutableMapOf<Long, Boolean>()
            for (tick in entries) if (tick.day == day) result[tick.habitId] = tick.done
            return result
        }

        /** Each habit's last queued entry for [day]. */
        fun lastByQueue(entries: List<HabitTick>, day: String): Map<Long, HabitTick> {
            val result = mutableMapOf<Long, HabitTick>()
            for (tick in entries) if (tick.day == day) result[tick.habitId] = tick
            return result
        }

        /** The same for one habit, by day. */
        fun doneByDay(entries: List<HabitTick>, habitId: Long): Map<String, Boolean> {
            val result = mutableMapOf<String, Boolean>()
            for (tick in entries) if (tick.habitId == habitId) result[tick.day] = tick.done
            return result
        }

        /** How the server's answer to a tick or untick is taken. */
        fun outcome(answer: DeviceApi.Answer?): TickSend = when {
            answer == null -> TickSend.Retry
            answer.status in 200..299 -> TickSend.Sent(runCatching { JSONObject(answer.body) }.getOrNull())
            // Refusals that sending again cannot change: the day closed or
            // was the wrong one, the habit is not scheduled or not there.
            answer.status == 409 || answer.status == 400 || answer.status == 404 ->
                TickSend.Refused(Budget.refusal(answer.body) ?: "The server refused this tick (${answer.status}).")
            // Signed out, rate limits, a relay that cannot reach the server.
            else -> TickSend.Retry
        }

        fun encode(entries: List<HabitTick>): String = JSONArray().apply {
            for (tick in entries) put(JSONObject()
                .put("key", tick.key).put("habit_id", tick.habitId).put("day", tick.day)
                .put("at", tick.at.toString()).put("action", tick.action).apply { if (tick.byRule) put("by", "rule") })
        }.toString()

        fun decode(json: String?): List<HabitTick> {
            val array = json?.let { runCatching { JSONArray(it) }.getOrNull() } ?: return emptyList()
            return (0 until array.length()).mapNotNull { index ->
                val item = array.optJSONObject(index) ?: return@mapNotNull null
                runCatching {
                    HabitTick(item.getString("key"), item.getLong("habit_id"), item.getString("day"),
                        Instant.parse(item.getString("at")), item.getString("action").takeIf { it == HabitTick.TICK || it == HabitTick.UNTICK }!!,
                        byRule = item.optString("by") == "rule")
                }.getOrNull()
            }
        }
    }
}

/**
 * The phone's tick queue, kept in its own preferences and written at once, so
 * a tick survives the app being closed or the phone restarting. Sent by the
 * app while it is open and by every collection cycle ([PolicyWatch]). As
 * Done on a reminder is ([FollowUpWatch]).
 */
object HabitTicks {
    private const val PREFS = "telemetry-habit-ticks"
    private const val KEY_QUEUE = "queue"
    private const val KEY_NOTICE = "notice"

    @Volatile private var queue: TickQueue? = null

    fun queue(context: Context): TickQueue = queue ?: synchronized(this) {
        queue ?: TickQueue(object : TickStore {
            private val preferences = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            override fun load(): String? = preferences.getString(KEY_QUEUE, null)
            override fun save(json: String) {
                preferences.edit().putString(KEY_QUEUE, json).commit()
                HabitWidgets.changed(context)
            }
        }).also { queue = it }
    }

    /** Keeps a tick or untick on the phone. It is sent with the next [flush]. */
    fun record(context: Context, habitId: Long, day: String, done: Boolean, at: Instant = Instant.now()) {
        queue(context).add(habitId, day, at, done)
    }

    /** Keeps a tick a habit rule made, at the moment the rule was met. */
    fun recordRule(context: Context, habitId: Long, day: String, at: Instant) {
        queue(context).add(habitId, day, at, done = true, byRule = true)
    }

    /**
     * Sends what is waiting, in order. A refusal's message is kept to be
     * shown once; the newest budget the server gave goes into the cached
     * answer. Blocking IO.
     */
    fun flush(context: Context): TickQueue.Flushed {
        val flushed = queue(context).flush { tick ->
            val body = JSONObject().put("day", tick.day).put("at", tick.at.toString()).apply { if (tick.byRule) put("by", "rule") }.toString()
            TickQueue.outcome(DeviceApi.send(context, "POST", "/v1/device/habits/${tick.habitId}/${tick.action}", body))
        }
        flushed.refusals.lastOrNull()?.let { message ->
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_NOTICE, message).apply()
        }
        flushed.budget?.let { NowCache.patchBudget(context, it) }
        return flushed
    }

    /** The message of the last refused tick, once: reading it clears it. */
    fun takeNotice(context: Context): String? {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val message = preferences.getString(KEY_NOTICE, null) ?: return null
        preferences.edit().remove(KEY_NOTICE).apply()
        return message
    }
}
