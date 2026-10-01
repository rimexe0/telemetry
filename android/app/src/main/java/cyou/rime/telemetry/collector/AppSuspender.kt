package cyou.rime.telemetry.collector

import android.content.Context
import android.content.pm.PackageManager
import java.util.concurrent.TimeUnit

/**
 * Optional, root-only: blocked apps are suspended the way Digital Wellbeing
 * pauses them, so they show gray and Android shows the reason when tapped.
 * The accessibility block screen stays as the fallback. Everything this
 * suspends is lifted as soon as it should not be blocked, when there is no
 * current decision, and when the setting is turned off.
 */
object AppSuspender {
    private const val PREFS = "telemetry-suspend"
    private const val KEY_ENABLED = "enabled"
    private const val KEY_SUSPENDED = "suspended"
    private const val KEY_MESSAGE = "message"
    private const val KEY_ERROR = "last_error"

    data class Plan(val suspend: Set<String>, val unsuspend: Set<String>)

    /**
     * What to change. Suspending again with a new reason updates the dialog,
     * so a changed message re-suspends everything still wanted.
     */
    fun plan(wanted: Set<String>, current: Set<String>, message: String, lastMessage: String?): Plan {
        val suspend = if (message != lastMessage) wanted else wanted - current
        return Plan(suspend = suspend, unsuspend = current - wanted)
    }

    /** Apps that should be suspended now: explicit blocks only, never gates. */
    fun wanted(decision: BlockDecision?, installed: (String) -> Boolean, systemSafe: Set<String>, ownPackage: String): Set<String> {
        if (decision == null || !decision.active) return emptySet()
        return decision.blocked
            .filter { it !in decision.alwaysAllowed && it !in systemSafe && it != ownPackage && installed(it) }
            .toSet()
    }

    fun enabled(context: Context): Boolean = prefs(context).getBoolean(KEY_ENABLED, false)

    fun lastError(context: Context): String? = prefs(context).getString(KEY_ERROR, null)

    /** Turns the option on only if root works; off always lifts every suspension. */
    fun setEnabled(context: Context, enabled: Boolean): Boolean {
        if (enabled && runRoot("id") != 0) {
            prefs(context).edit().putBoolean(KEY_ENABLED, false).putString(KEY_ERROR, "Root access was not granted.").apply()
            return false
        }
        prefs(context).edit().putBoolean(KEY_ENABLED, enabled).remove(KEY_ERROR).apply()
        if (!enabled) reconcile(context, null)
        return true
    }

    /** Brings the suspended set in line with the decision in force. */
    fun reconcile(context: Context, decision: BlockDecision?) {
        val preferences = prefs(context)
        val current = preferences.getStringSet(KEY_SUSPENDED, emptySet()).orEmpty()
        val wanted = if (enabled(context)) {
            wanted(decision, { installed(context, it) }, PolicyEvents.systemSafe(context), context.packageName)
        } else {
            emptySet()
        }
        val message = decision?.reason?.takeIf { wanted.isNotEmpty() } ?: ""
        val plan = plan(wanted, current, message, preferences.getString(KEY_MESSAGE, null))
        if (plan.suspend.isEmpty() && plan.unsuspend.isEmpty()) return
        var suspended = current
        var error: String? = null
        if (plan.unsuspend.isNotEmpty()) {
            if (runRoot("pm unsuspend ${plan.unsuspend.joinToString(" ")}") == 0) {
                suspended = suspended - plan.unsuspend
                PolicyEvents.record(context, "android.policy_unsuspend", "unsuspend:${System.currentTimeMillis()}", decision, mapOf("packages" to plan.unsuspend.joinToString(",")))
            } else {
                error = "Could not lift the suspension of ${plan.unsuspend.joinToString()}."
            }
        }
        if (plan.suspend.isNotEmpty()) {
            val dialog = "Closed during ${NowModel.modeLabel(decision?.mode ?: "")}. $message".trim()
            if (runRoot("pm suspend --dialogMessage ${quote(dialog)} ${plan.suspend.joinToString(" ")}") == 0) {
                suspended = suspended + plan.suspend
                PolicyEvents.record(context, "android.policy_suspend", "suspend:${System.currentTimeMillis()}", decision, mapOf("packages" to plan.suspend.joinToString(",")))
            } else {
                error = "Could not suspend ${plan.suspend.joinToString()}; the block screen still applies."
            }
        }
        preferences.edit().putStringSet(KEY_SUSPENDED, suspended).putString(KEY_MESSAGE, message).putString(KEY_ERROR, error).apply()
    }

    fun quote(value: String): String = "'" + value.replace("'", "'\\''") + "'"

    private fun installed(context: Context, pkg: String): Boolean =
        runCatching { context.packageManager.getPackageInfo(pkg, 0); true }.getOrElse { it !is PackageManager.NameNotFoundException }

    private fun runRoot(command: String): Int = runCatching {
        val process = ProcessBuilder("su", "-c", command).redirectErrorStream(true).start()
        process.inputStream.bufferedReader().readText()
        if (process.waitFor(15, TimeUnit.SECONDS)) process.exitValue() else { process.destroy(); -1 }
    }.getOrDefault(-1)

    private fun prefs(context: Context) = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
