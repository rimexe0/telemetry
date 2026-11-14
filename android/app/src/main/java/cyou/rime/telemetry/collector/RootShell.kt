package cyou.rime.telemetry.collector

import java.util.concurrent.TimeUnit

/** Runs one shell command as root and returns its exit code and output. */
internal object RootShell {
    fun run(command: String, timeoutSeconds: Long = 15): Pair<Int, String> = runCatching {
        val process = ProcessBuilder("su", "-c", command).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().use { it.readText() }
        if (process.waitFor(timeoutSeconds, TimeUnit.SECONDS)) {
            process.exitValue() to output
        } else {
            process.destroy()
            -1 to output
        }
    }.getOrDefault(-1 to "")
}
