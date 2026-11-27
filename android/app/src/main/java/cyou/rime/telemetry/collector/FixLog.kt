package cyou.rime.telemetry.collector

import android.content.Context
import java.io.File
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/**
 * The phone's own GPS positions, kept on the phone by local day, so the
 * Places map draws at once and without the server. The positions also go
 * to the server as before; these copies are only read here. One line per
 * position: epoch millis, latitude, longitude, accuracy in meters.
 */
class FixLog(private val dir: File) {
    companion object {
        const val KEEP_DAYS = 30L

        @Volatile private var shared: FixLog? = null

        fun of(context: Context): FixLog = shared ?: synchronized(this) {
            shared ?: FixLog(File(context.applicationContext.filesDir, "fixes")).also { shared = it }
        }

        fun parseLine(line: String): RuleFix? {
            val parts = line.split(',')
            if (parts.size != 4) return null
            val at = parts[0].toLongOrNull() ?: return null
            val lat = parts[1].toDoubleOrNull() ?: return null
            val lon = parts[2].toDoubleOrNull() ?: return null
            val accuracy = parts[3].toDoubleOrNull() ?: return null
            if (lat !in -90.0..90.0 || lon !in -180.0..180.0) return null
            return RuleFix(Instant.ofEpochMilli(at), lat, lon, accuracy)
        }
    }

    private fun file(day: LocalDate) = File(dir, "$day.csv")

    @Synchronized
    fun record(fix: RuleFix, zone: ZoneId = ZoneId.systemDefault()) {
        dir.mkdirs()
        val day = fix.at.atZone(zone).toLocalDate()
        file(day).appendText("${fix.at.toEpochMilli()},${fix.lat},${fix.lon},${fix.accuracy}\n")
        prune(day)
    }

    /** The positions recorded from [from] to [to], oldest first. */
    @Synchronized
    fun between(from: Instant, to: Instant, zone: ZoneId = ZoneId.systemDefault()): List<RuleFix> {
        val first = from.atZone(zone).toLocalDate()
        val last = to.atZone(zone).toLocalDate()
        val fixes = mutableListOf<RuleFix>()
        var day = first
        while (!day.isAfter(last)) {
            val file = file(day)
            if (file.isFile) runCatching { file.readLines() }.getOrNull()?.mapNotNullTo(fixes, ::parseLine)
            day = day.plusDays(1)
        }
        return fixes.filter { !it.at.isBefore(from) && it.at.isBefore(to) }.sortedBy { it.at }
    }

    private var prunedFor: LocalDate? = null

    private fun prune(today: LocalDate) {
        if (prunedFor == today) return
        prunedFor = today
        val oldest = today.minusDays(KEEP_DAYS)
        dir.listFiles()?.forEach { file ->
            val day = runCatching { LocalDate.parse(file.name.removeSuffix(".csv")) }.getOrNull() ?: return@forEach
            if (day.isBefore(oldest)) file.delete()
        }
    }
}
