package cyou.rime.telemetry.collector

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class WatchWalksTest {
    @Test
    fun aWatchWalkBecomesOneObservation() {
        val walk = JSONObject().put("session_id", "w1").put("start", "2026-10-15T07:00:00Z").put("end", "2026-10-15T07:20:00Z")
            .put("points", JSONArray().put(JSONObject().put("t", "2026-10-15T07:01:00Z").put("lat", 38.4).put("lon", 17.1).put("acc", 8).put("worn", true)))
        val observation = WatchWalks.observation("android-1", walk, Instant.parse("2026-10-15T07:30:00Z"))
        assertEquals("watch.outside_walk", observation.kind)
        assertEquals("walk:w1", observation.sourceRecordKey)
        assertEquals("2026-10-15T07:20:00Z", observation.endedAt)
        assertTrue(observation.payloadJson.contains("\"worn\":true"))
    }

    @Test(expected = IllegalArgumentException::class)
    fun anEmptyWalkIsRefused() {
        WatchWalks.observation("android-1", JSONObject().put("session_id", "w2").put("start", "2026-10-15T07:00:00Z")
            .put("end", "2026-10-15T07:00:00Z").put("points", JSONArray()), Instant.now())
    }
}
