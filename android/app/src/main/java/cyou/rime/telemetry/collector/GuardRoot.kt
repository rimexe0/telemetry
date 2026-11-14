package cyou.rime.telemetry.collector

import android.content.ComponentName
import android.content.Context
import java.io.File
import java.time.Instant

/**
 * Optional, root-only watcher. A small shell loop started with su puts the
 * block screen and the gray-outs back when they are switched off in Android's
 * settings, and writes each removal it finds to a log the app records as a
 * bypass. It only re-applies what the approved policy already decided, checks
 * every fifteen seconds, and touches the system only when something changed.
 */
object GuardRoot {
    private const val PREFS = "telemetry-guard"
    private const val KEY_ENABLED = "enabled"
    private const val KEY_ERROR = "last_error"
    private const val DIR = "guard"
    private const val SCRIPT = "guard.sh"
    private const val PID = "guard.pid"
    private const val LOG = "guard.log"
    private const val ATTEMPTS = "attempts.log"
    private const val PAUSE = "pause"
    private const val STOP = "stop"

    fun enabled(context: Context): Boolean = prefs(context).getBoolean(KEY_ENABLED, false)

    fun lastError(context: Context): String? = prefs(context).getString(KEY_ERROR, null)

    /** On only when root answers; off stops the loop at once. */
    fun setEnabled(context: Context, enabled: Boolean): Boolean {
        if (enabled) {
            if (RootShell.run("id").first != 0) {
                prefs(context).edit().putBoolean(KEY_ENABLED, false)
                    .putString(KEY_ERROR, "Root access was not granted.").apply()
                return false
            }
            prefs(context).edit().putBoolean(KEY_ENABLED, true).remove(KEY_ERROR).apply()
            syncWithCache(context)
            return true
        }
        prefs(context).edit().putBoolean(KEY_ENABLED, false).remove(KEY_ERROR).apply()
        stop(context)
        return true
    }

    /** Re-applies the newest cached decision. Cheap when nothing changed. */
    fun syncWithCache(context: Context) {
        val decision = NowCache.read(context)?.let {
            runCatching { BlockPolicy.current(context, it.json) }.getOrNull()
        }
        sync(context, decision)
    }

    /** After an update or a reboot, start the loop again if it is wanted. */
    fun ensureRunning(context: Context) {
        if (!enabled(context)) return
        val app = context.applicationContext
        // su may ask for permission; never on the caller's main thread.
        Thread { runCatching { syncWithCache(app) } }.start()
    }

    /** The loop starts the watcher again only after this moment has passed. */
    fun pause(context: Context, until: Instant) {
        val dir = File(context.filesDir, DIR).apply { mkdirs() }
        File(dir, PAUSE).writeText(until.epochSecond.toString())
    }

    /**
     * Writes the loop for the current decision and starts it, or leaves a
     * running loop alone when its work is unchanged.
     */
    @Synchronized
    fun sync(context: Context, decision: BlockDecision?) {
        if (!enabled(context)) {
            stop(context)
            return
        }
        if (paused(context)) return
        val dir = File(context.filesDir, DIR)
        val suspended = if (AppSuspender.enabled(context)) {
            AppSuspender.wanted(decision, { AppSuspender.installed(context, it) }, PolicyEvents.systemSafe(context), context.packageName)
        } else {
            emptySet()
        }
        val message = decision?.reason?.takeIf { suspended.isNotEmpty() }.orEmpty()
        val component = ComponentName(context, TelemetryAccessibilityService::class.java).flattenToString()
        val script = GuardScript.build(dir.absolutePath, component, suspended, message)
        val file = File(dir, SCRIPT)
        if (file.exists() && file.readText() == script && running(context)) return
        dir.mkdirs()
        file.writeText(script)
        restart(context, file)
    }

    /** Records the removals the loop found since the last check. */
    fun collectAttempts(context: Context) {
        val dir = File(context.filesDir, DIR)
        val live = File(dir, ATTEMPTS)
        if (!live.exists()) return
        val pending = File(dir, ATTEMPTS + ".read")
        // One atomic rename: the loop writes a fresh file after this.
        if (!live.renameTo(pending)) return
        val lines = runCatching { pending.readLines() }.getOrDefault(emptyList()).filter { it.isNotBlank() }
        pending.delete()
        lines.forEach { line ->
            val parts = line.trim().split(' ', limit = 2)
            val at = parts.firstOrNull()?.toLongOrNull() ?: return@forEach
            val cause = parts.getOrNull(1) ?: "tamper"
            PolicyEvents.record(
                context,
                "android.policy_tamper",
                "tamper:$at:$cause",
                null,
                mapOf("cause" to cause, "at" to Instant.ofEpochSecond(at).toString()),
            )
        }
    }

    private fun paused(context: Context, now: Instant = Instant.now()): Boolean {
        val file = File(File(context.filesDir, DIR), PAUSE)
        val until = file.takeIf { it.exists() }?.readText()?.trim()?.toLongOrNull() ?: return false
        if (now.epochSecond < until) return true
        file.delete()
        return false
    }

    private fun running(context: Context): Boolean {
        val pid = File(File(context.filesDir, DIR), PID).takeIf { it.exists() }?.readText()?.trim()
        if (pid.isNullOrBlank()) return false
        return RootShell.run("kill -0 $pid").first == 0
    }

    private fun restart(context: Context, script: File) {
        stop(context)
        File(script.parentFile, STOP).delete()
        val log = File(script.parentFile, LOG)
        val (code, _) = RootShell.run(
            "nohup sh '${script.absolutePath}' >> '${log.absolutePath}' 2>&1 </dev/null &",
        )
        if (code != 0) prefs(context).edit().putString(KEY_ERROR, "The watcher did not start.").apply()
    }

    private fun stop(context: Context) {
        val dir = File(context.filesDir, DIR).apply { mkdirs() }
        // The file ends the loop even when the kill does not reach it.
        File(dir, STOP).writeText("1")
        val pid = File(dir, PID).takeIf { it.exists() }?.readText()?.trim()
        if (!pid.isNullOrBlank()) RootShell.run("kill $pid")
        File(dir, PID).delete()
    }

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}

/** The shell loop the watcher runs, built for one decision. */
internal object GuardScript {
    // The gray-outs are re-applied once a minute; the block screen every tick.
    private const val SUSPEND_EVERY = 4

    fun build(dir: String, component: String, packages: Set<String>, message: String): String {
        val script = StringBuilder()
        script.line("#!/system/bin/sh")
        script.line("# Written by Telemetry. Puts the block screen and the gray-outs back")
        script.line("# when they are switched off, and notes that it did.")
        script.line("dir=${AppSuspender.quote(dir)}")
        script.line("echo \$\$ > \"\$dir/guard.pid\"")
        script.line("component=${AppSuspender.quote(component)}")
        script.line("note() {")
        script.line("  echo \"\$(date +%s) \$*\" >> \"\$dir/attempts.log\"")
        script.line("  if [ \"\$(wc -l < \"\$dir/attempts.log\")\" -gt 200 ]; then tail -n 100 \"\$dir/attempts.log\" > \"\$dir/attempts.tmp\"; mv \"\$dir/attempts.tmp\" \"\$dir/attempts.log\"; fi")
        script.line("}")
        script.line("tick=0")
        script.line("while [ ! -f \"\$dir/stop\" ]; do")
        script.line("  if [ -f \"\$dir/pause\" ]; then")
        script.line("    until=\$(cat \"\$dir/pause\" 2>/dev/null)")
        script.line("    if [ -z \"\$until\" ] || [ \"\$(date +%s)\" -lt \"\$until\" ]; then sleep 5; continue; fi")
        script.line("    rm -f \"\$dir/pause\"")
        script.line("  fi")
        script.line("  current=\$(settings get secure enabled_accessibility_services 2>/dev/null)")
        script.line("  case \":\$current:\" in")
        script.line("    *:\"\$component\":*) ;;")
        script.line("    *)")
        script.line("      if [ -z \"\$current\" ] || [ \"\$current\" = \"null\" ]; then")
        script.line("        settings put secure enabled_accessibility_services \"\$component\" 2>/dev/null")
        script.line("      else")
        script.line("        settings put secure enabled_accessibility_services \"\$current:\$component\" 2>/dev/null")
        script.line("      fi")
        script.line("      note block_screen_off")
        script.line("      ;;")
        script.line("  esac")
        script.line("  if [ \"\$(settings get secure accessibility_enabled 2>/dev/null)\" != \"1\" ]; then")
        script.line("    settings put secure accessibility_enabled 1 2>/dev/null")
        script.line("    note accessibility_off")
        script.line("  fi")
        if (packages.isNotEmpty()) {
            script.line("  tick=\$((tick + 1))")
            script.line("  if [ \"\$tick\" -ge $SUSPEND_EVERY ]; then")
            script.line("    tick=0")
            script.line("    for p in ${packages.sorted().joinToString(" ")}; do")
            script.line("      if ! dumpsys package \"\$p\" 2>/dev/null | grep -q 'suspended=true'; then")
            script.line("        pm suspend --dialogMessage ${AppSuspender.quote(flat(message))} \"\$p\" >/dev/null 2>&1")
            script.line("        note unsuspended:\$p")
            script.line("      fi")
            script.line("    done")
            script.line("  fi")
        }
        script.line("  sleep 15")
        script.line("done")
        script.line("rm -f \"\$dir/guard.pid\"")
        return script.toString()
    }

    private fun StringBuilder.line(value: String) {
        append(value).append('\n')
    }

    private fun flat(message: String): String = message.replace('\n', ' ').replace('\r', ' ').trim()
}
