package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class InsightsTest {
    private val json = """{"date":"2026-10-01","insights":[
        {"key":"distractions","text":"50m on distractions today","detail":"Mostly com.twitter.android: 50m in 2 visits.","names":["com.twitter.android"]},
        {"key":"screen_mac","text":"Mac: 2h of screen time","detail":"Top app: Code, 2h.","names":["Code"]},
        {"key":"clean_run","text":"Longest run without a distraction: 1h 30m"}]}"""

    @Test
    fun namesInstalledAppsAndKeepsOtherNames() {
        val insights = Insights.parse(json) { raw -> if (raw == "com.twitter.android") "X" else null }
        assertEquals(3, insights.size)
        assertEquals("Mostly X: 50m in 2 visits.", insights[0].detail)
        assertEquals("Top app: Code, 2h.", insights[1].detail)
        assertNull(insights[2].detail)
    }

    @Test
    fun onlyTodaysAnswerIsShown() {
        val zone = ZoneId.of("Europe/Istanbul")
        assertTrue(Insights.isToday(json, Instant.parse("2026-10-01T20:59:00Z"), zone))
        assertFalse(Insights.isToday(json, Instant.parse("2026-10-01T21:01:00Z"), zone))
        assertFalse(Insights.isToday("not json", Instant.now(), zone))
    }
}
