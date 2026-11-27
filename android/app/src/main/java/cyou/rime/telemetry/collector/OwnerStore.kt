package cyou.rime.telemetry.collector

import android.content.Context
import java.io.File
import java.time.Instant
import java.time.LocalDate

/**
 * The server's answers for Activity and Settings, kept on the phone as they
 * came, with when they came, so each screen shows at once and keeps working
 * while the server is down. One file per answer; per-day answers are
 * dropped once they are older than [KEEP_DAYS].
 */
class AnswerStore(private val dir: File) {
    data class Entry(val json: String, val at: Instant)

    companion object {
        const val KEEP_DAYS = 21L
        private val safe = Regex("[A-Za-z0-9._-]{1,80}")
        private val dayKey = Regex(".*-(\\d{4}-\\d{2}-\\d{2})")

        @Volatile private var shared: AnswerStore? = null

        fun of(context: Context): AnswerStore = shared ?: synchronized(this) {
            shared ?: AnswerStore(File(context.applicationContext.filesDir, "answers")).also { shared = it }
        }
    }

    private fun file(key: String): File {
        require(safe.matches(key)) { "bad key $key" }
        return File(dir, "$key.json")
    }

    @Synchronized
    fun read(key: String): Entry? {
        val file = file(key)
        if (!file.isFile) return null
        val json = runCatching { file.readText() }.getOrNull() ?: return null
        return Entry(json, Instant.ofEpochMilli(file.lastModified()))
    }

    /** Keeps an answer; written whole to a new file first, so a crash never leaves half of one. */
    @Synchronized
    fun put(key: String, json: String, at: Instant = Instant.now()) {
        dir.mkdirs()
        val target = file(key)
        val temp = File(dir, "$key.json.new")
        temp.writeText(json)
        temp.setLastModified(at.toEpochMilli())
        if (!temp.renameTo(target)) {
            target.delete()
            temp.renameTo(target)
        }
        target.setLastModified(at.toEpochMilli())
    }

    /** Drops per-day answers ("places-2026-11-01") for days before [today] minus [KEEP_DAYS]. */
    @Synchronized
    fun prune(today: LocalDate) {
        val oldest = today.minusDays(KEEP_DAYS)
        dir.listFiles()?.forEach { file ->
            val day = dayKey.matchEntire(file.name.removeSuffix(".json"))?.groupValues?.get(1) ?: return@forEach
            val date = runCatching { LocalDate.parse(day) }.getOrNull() ?: return@forEach
            if (date.isBefore(oldest)) file.delete()
        }
    }
}
