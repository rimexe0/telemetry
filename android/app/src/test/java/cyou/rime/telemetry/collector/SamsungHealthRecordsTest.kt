package cyou.rime.telemetry.collector

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class SamsungHealthRecordsTest {
    private val now = Instant.parse("2026-10-15T08:00:00Z")

    private fun walk(minutes: Int) = SamsungRecord("exercise:a:0", "exercise", now.minusSeconds(3600), now.minusSeconds(3600L - minutes * 60),
        JSONObject().put("exercise_type", "WALKING").put("duration_seconds", minutes * 60))

    @Test
    fun sendsOnlyNewOrChangedRecords() {
        val (first, sent) = SamsungHealthRecords.changed(listOf(walk(18)), JSONObject(), now)
        assertEquals(1, first.size)
        val (again, _) = SamsungHealthRecords.changed(listOf(walk(18)), sent, now)
        assertTrue("an unchanged walk is not sent again", again.isEmpty())
        val (revised, _) = SamsungHealthRecords.changed(listOf(walk(22)), sent, now)
        assertEquals("a walk Samsung Health extended is sent again", 1, revised.size)
    }

    @Test
    fun forgetsWhatIsOlderThanTheWindow() {
        val old = SamsungRecord("sleep:x", "sleep", now.minusSeconds(10 * 86400), now.minusSeconds(10 * 86400 - 3600), JSONObject())
        val (_, sent) = SamsungHealthRecords.changed(listOf(old, walk(18)), JSONObject(), now)
        assertEquals(setOf("exercise:a:0"), sent.keys().asSequence().toSet())
    }

    @Test
    fun groupsStepsByHour() {
        val records = SamsungHealthRecords.hourlySteps(listOf(
            Instant.parse("2026-10-15T07:10:00Z") to 40L,
            Instant.parse("2026-10-15T07:11:00Z") to 0L,
            Instant.parse("2026-10-15T07:59:00Z") to 60L,
            Instant.parse("2026-10-15T08:00:00Z") to 5L,
        ))
        assertEquals(2, records.size)
        assertEquals(100L, records[0].payload.getLong("total"))
        assertEquals(2, records[0].payload.getJSONArray("minutes").length())
        assertEquals("steps:${Instant.parse("2026-10-15T07:00:00Z").toEpochMilli()}", records[0].key)
    }

    @Test
    fun thinsLongRoutesAndKeepsTheEnd() {
        val points = (1..1500).toList()
        val thinned = SamsungHealthRecords.thin(points, 600)
        assertTrue(thinned.size <= 601)
        assertEquals(1, thinned.first())
        assertEquals(1500, thinned.last())
        assertEquals(points.take(10), SamsungHealthRecords.thin(points.take(10), 600))
    }

    @Test
    fun observationsCarryTheirSource() {
        val observation = SamsungHealthRecords.observations("android-1", listOf(walk(18)), now).single()
        assertEquals("android:android-1:samsung_health", observation.sourceInstanceKey)
        assertEquals("samsung_health.exercise", observation.kind)
    }
}
