package cyou.rime.telemetry.collector

import android.content.Context
import java.time.Duration
import java.time.Instant

/**
 * Emergency unlock (#25): everything blocked opens for a few minutes, at once
 * and on this phone alone, so it works with no server, network or assistant.
 * It is recorded, so the server opens the Mac too and the assistant can see
 * afterwards what it was used for. Nobody asks about it.
 */
object Emergency {
    private const val PREFS = "telemetry-emergency"
    private const val KEY_UNTIL = "until"
    val LENGTH: Duration = Duration.ofMinutes(10)

    fun until(context: Context): Instant? =
        prefs(context).getLong(KEY_UNTIL, 0L).takeIf { it > 0 }?.let(Instant::ofEpochMilli)

    fun active(context: Context, now: Instant = Instant.now()): Boolean = until(context)?.isAfter(now) == true

    /** Opens everything until now plus [LENGTH], lifts suspensions and records it. Blocking IO. */
    fun start(context: Context, now: Instant = Instant.now()) {
        val until = now.plus(LENGTH)
        prefs(context).edit().putLong(KEY_UNTIL, until.toEpochMilli()).commit()
        PolicyEvents.record(context, "android.policy_emergency", "emergency:${now.toEpochMilli()}", null,
            mapOf("started_at" to now.toString(), "until" to until.toString()))
        runCatching { AppSuspender.reconcile(context, null) }
    }

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}

/** The decision in force on this phone: none during an emergency unlock. */
fun BlockPolicy.current(context: Context, json: String, now: Instant = Instant.now()): BlockDecision? =
    if (Emergency.active(context, now)) null else runCatching { at(json, now) }.getOrNull()
