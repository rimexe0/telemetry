package cyou.rime.telemetry.collector

import android.content.Context
import android.provider.Settings
import java.util.concurrent.TimeUnit

/**
 * Turns the screen grayscale while the decision asks for it (the wind-down
 * before bedtime) and back afterwards, using Android's own monochrome display
 * mode. A grayscale the owner turned on is left alone: only what this app
 * turned on is turned off again.
 *
 * Switched off while it is wanted, it used to come back at the next check
 * (2026-11-24). With the budget on (2026-11-26) the phone asks once instead,
 * with a small notice from the accessibility service: keep it off at the
 * surcharge until grayscale is no longer wanted, or switch back. Turning it
 * on again later reports a restore, which ends the surcharge. With the budget
 * off, or without the accessibility service to ask, it comes back as before.
 */
object Grayscale {
    private const val PREFS = "telemetry-grayscale"
    private const val KEY_OWNED = "owned"
    private const val KEY_PREVIOUS_MODE = "previous_mode"
    /** Kept off from this phone, at the surcharge, for this wind-down. */
    private const val KEY_DECLINED = "declined"
    /** Turned back on, and the server not told yet. */
    private const val KEY_RESTORE_PENDING = "restore_pending"
    const val ENABLED = "accessibility_display_daltonizer_enabled"
    private const val MODE = "accessibility_display_daltonizer"
    private const val MONOCHROME = 0

    /** What to do now: turn it on, turn it off, ask, report it back on, or nothing. */
    enum class Step { ON, OFF, ASK, RESTORE, NONE }

    /**
     * [declinedHere] is a decline made on this phone; [declinedElsewhere] one
     * the server reports from another device, which stops the asking but is
     * not this phone's to restore. [ask] is whether the owner can be asked.
     */
    fun step(
        wanted: Boolean,
        owned: Boolean,
        screenGray: Boolean,
        declinedHere: Boolean = false,
        declinedElsewhere: Boolean = false,
        ask: Boolean = false,
    ): Step = when {
        // Turned on again after keeping it off: the surcharge ends.
        wanted && screenGray -> if (declinedHere) Step.RESTORE else Step.NONE
        // Kept off at a cost; left as the owner chose.
        wanted && owned && (declinedHere || declinedElsewhere) -> Step.NONE
        // Switched off while wanted: ask rather than switch it back silently.
        wanted && owned && ask -> Step.ASK
        // Turn it on, or put it back after it was switched off.
        wanted -> Step.ON
        owned && screenGray -> Step.OFF
        else -> Step.NONE
    }

    fun reconcile(context: Context, decision: BlockDecision?) {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val wanted = decision?.grayscale == true
        val owned = prefs.getBoolean(KEY_OWNED, false)
        if (!wanted) {
            // The wind-down is over: a decline ends with it.
            if (prefs.getBoolean(KEY_DECLINED, false) || prefs.getBoolean(KEY_RESTORE_PENDING, false)) {
                prefs.edit().remove(KEY_DECLINED).remove(KEY_RESTORE_PENDING).apply()
            }
            TelemetryAccessibilityService.connected?.dismissGrayscaleNotice()
            if (!owned) return
        }
        if (wanted && prefs.getBoolean(KEY_RESTORE_PENDING, false)) reportRestore(context)
        val resolver = context.contentResolver
        val gray = screenGray(context)
        val budget = NowCache.budget(context)?.takeIf { it.enabled }
        val service = TelemetryAccessibilityService.connected
        val step = step(
            wanted = wanted,
            owned = owned,
            screenGray = gray,
            declinedHere = prefs.getBoolean(KEY_DECLINED, false),
            declinedElsewhere = budget?.grayscaleDeclined == true,
            ask = budget != null && service != null,
        )
        when (step) {
            Step.NONE -> {
                if (gray) service?.dismissGrayscaleNotice()
                if (wanted && gray) return
            }
            Step.ASK -> {
                service?.askGrayscale(budget?.grayscaleSurcharge)
                return
            }
            Step.RESTORE -> {
                service?.dismissGrayscaleNotice()
                prefs.edit().remove(KEY_DECLINED).putBoolean(KEY_RESTORE_PENDING, true).commit()
                reportRestore(context)
                return
            }
            Step.ON -> {
                if (!owned) prefs.edit().putInt(KEY_PREVIOUS_MODE, Settings.Secure.getInt(resolver, MODE, -1)).apply()
                if (!turnOn(context)) return
            }
            Step.OFF -> {
                val previous = prefs.getInt(KEY_PREVIOUS_MODE, -1)
                if (!put(context, ENABLED, 0)) return
                if (previous >= 0) put(context, MODE, previous)
            }
        }
        prefs.edit().putBoolean(KEY_OWNED, wanted).apply()
    }

    fun screenGray(context: Context): Boolean {
        val resolver = context.contentResolver
        return Settings.Secure.getInt(resolver, ENABLED, 0) == 1 && Settings.Secure.getInt(resolver, MODE, -1) == MONOCHROME
    }

    /** "Switch back", or no answer to the notice: grayscale on again. */
    fun switchBack(context: Context) {
        if (turnOn(context)) context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(KEY_OWNED, true).apply()
    }

    /**
     * "Keep it off": reported to the server, which charges the surcharge.
     * When the server did not record it, grayscale comes back, since without
     * the server nothing can be kept off at a cost. Blocking IO.
     */
    fun keepOff(context: Context): Door.Outcome {
        val outcome = Door.grayscale(context, "decline")
        if (outcome is Door.Outcome.Done) {
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(KEY_DECLINED, true).commit()
        } else {
            switchBack(context)
        }
        return outcome
    }

    private val restoring = java.util.concurrent.atomic.AtomicBoolean(false)

    private fun reportRestore(context: Context) {
        if (!restoring.compareAndSet(false, true)) return
        // Blocking IO, and reconcile may run on the main thread.
        val app = context.applicationContext
        Thread {
            try {
                if (Door.grayscale(app, "restore") !is Door.Outcome.Unreachable) {
                    app.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().remove(KEY_RESTORE_PENDING).apply()
                }
            } finally {
                restoring.set(false)
            }
        }.start()
    }

    private fun turnOn(context: Context): Boolean = put(context, MODE, MONOCHROME) && put(context, ENABLED, 1)

    /** Writes a secure setting with the granted permission, else with root. */
    private fun put(context: Context, key: String, value: Int): Boolean {
        if (runCatching { Settings.Secure.putInt(context.contentResolver, key, value) }.getOrDefault(false)) return true
        return runCatching {
            val process = ProcessBuilder("su", "-c", "settings put secure $key $value").redirectErrorStream(true).start()
            process.inputStream.bufferedReader().readText()
            process.waitFor(10, TimeUnit.SECONDS) && process.exitValue() == 0
        }.getOrDefault(false)
    }
}
