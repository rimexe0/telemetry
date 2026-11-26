package cyou.rime.telemetry.collector

import android.content.Context
import java.time.Instant

/**
 * Emergency unlock (#25), superseded on 2026-11-26 by the door. The phone no
 * longer offers starting one, but an emergency started before this version
 * is still honoured until it runs out: everything stays open on this phone,
 * as it was promised when it started.
 */
object Emergency {
    private const val PREFS = "telemetry-emergency"
    private const val KEY_UNTIL = "until"

    fun until(context: Context): Instant? =
        prefs(context).getLong(KEY_UNTIL, 0L).takeIf { it > 0 }?.let(Instant::ofEpochMilli)

    fun active(context: Context, now: Instant = Instant.now()): Boolean = until(context)?.isAfter(now) == true

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}

/** The decision in force on this phone: none during an emergency unlock that is already running. */
fun BlockPolicy.current(context: Context, json: String, now: Instant = Instant.now()): BlockDecision? =
    if (Emergency.active(context, now)) null else runCatching { at(json, now) }.getOrNull()
