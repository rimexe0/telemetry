package cyou.rime.telemetry.wear

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class WalkLogTest {
    private val start = Instant.parse("2026-10-15T07:00:00Z")

    @Test
    fun recordsPointsWornAndSteps() {
        val log = WalkLog("w1", start, "phone_out_of_reach")
        for (minute in 0..10) log.add(WalkPoint(start.plusSeconds(minute * 60L), 38.4 + minute * 0.0005, 17.1, 8f, minute < 6, 110))
        log.add(WalkPoint(start.plusSeconds(600), 38.5, 17.1, 8f, true, null))
        log.addSteps(1200)
        log.end(start.plusSeconds(660))
        assertEquals(11, log.size)
        assertEquals(5, log.wornMinutes())
        val json = log.toJson()
        assertEquals(1200, json.getLong("steps"))
        assertEquals("phone_out_of_reach", json.getString("trigger"))
        assertFalse(json.getJSONArray("points").getJSONObject(8).getBoolean("worn"))
        assertEquals(110, json.getJSONArray("points").getJSONObject(0).getInt("bpm"))
    }

    @Test
    fun thinsLongWalksAndKeepsTheLastPoint() {
        val kept = WalkLog.thin((1..2000).toList(), 900)
        assertTrue(kept.size <= 901)
        assertEquals(2000, kept.last())
    }

    @Test
    fun gpsPausesWithoutSteps() {
        assertTrue(GpsPacing.shouldRun(start, start.plusSeconds(9 * 60)))
        assertFalse(GpsPacing.shouldRun(start, start.plusSeconds(10 * 60)))
    }

    @Test
    fun wornFromTheOffBodySensorOrARecentHeartRate() {
        assertTrue(Wearing.worn(true, null, start))
        assertFalse(Wearing.worn(false, start, start))
        assertTrue(Wearing.worn(null, start.minusSeconds(60), start))
        assertFalse(Wearing.worn(null, start.minusSeconds(120), start))
        assertFalse(Wearing.worn(null, null, start))
    }
}
