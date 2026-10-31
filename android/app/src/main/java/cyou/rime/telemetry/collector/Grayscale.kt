package cyou.rime.telemetry.collector

import android.content.Context
import android.provider.Settings
import java.util.concurrent.TimeUnit

/**
 * Turns the screen grayscale while the decision asks for it (the wind-down
 * before bedtime) and back afterwards, using Android's own monochrome display
 * mode. A grayscale the owner turned on is left alone: only what this app
 * turned on is turned off again.
 */
object Grayscale {
    private const val PREFS = "telemetry-grayscale"
    private const val KEY_OWNED = "owned"
    private const val KEY_PREVIOUS_MODE = "previous_mode"
    private const val ENABLED = "accessibility_display_daltonizer_enabled"
    private const val MODE = "accessibility_display_daltonizer"
    private const val MONOCHROME = 0

    /** What to do now: turn it on, turn it off, or nothing. */
    enum class Step { ON, OFF, NONE }

    fun step(wanted: Boolean, owned: Boolean, screenGray: Boolean): Step = when {
        wanted == owned -> Step.NONE
        wanted && screenGray -> Step.NONE
        wanted -> Step.ON
        screenGray -> Step.OFF
        else -> Step.NONE
    }

    fun reconcile(context: Context, decision: BlockDecision?) {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val wanted = decision?.grayscale == true
        val owned = prefs.getBoolean(KEY_OWNED, false)
        if (wanted == owned) return
        val resolver = context.contentResolver
        val enabled = Settings.Secure.getInt(resolver, ENABLED, 0) == 1
        val gray = enabled && Settings.Secure.getInt(resolver, MODE, -1) == MONOCHROME
        when (step(wanted, owned, gray)) {
            Step.NONE -> if (wanted && gray) return
            Step.ON -> {
                prefs.edit().putInt(KEY_PREVIOUS_MODE, Settings.Secure.getInt(resolver, MODE, -1)).apply()
                if (!put(context, MODE, MONOCHROME) || !put(context, ENABLED, 1)) return
            }
            Step.OFF -> {
                val previous = prefs.getInt(KEY_PREVIOUS_MODE, -1)
                if (!put(context, ENABLED, 0)) return
                if (previous >= 0) put(context, MODE, previous)
            }
        }
        prefs.edit().putBoolean(KEY_OWNED, wanted).apply()
    }

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
