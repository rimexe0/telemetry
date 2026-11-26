package cyou.rime.telemetry.collector

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class AppLabelsTest {
    @Test
    fun onlyNewAndChangedLabelsAreSent() {
        val sent = mapOf("com.whatsapp" to "WhatsApp", "com.twitter.android" to "Twitter")
        val seen = mapOf("com.whatsapp" to "WhatsApp", "com.twitter.android" to "X", "org.mozilla.firefox" to "Firefox", "com.blank" to " ")
        assertEquals(mapOf("com.twitter.android" to "X", "org.mozilla.firefox" to "Firefox"), AppLabels.changed(sent, seen))
        assertTrue(AppLabels.changed(sent, sent).isEmpty())
    }

    @Test
    fun theObservationCarriesPackageToLabel() {
        val at = Instant.parse("2026-11-26T10:00:00Z")
        val observation = AppLabels.observation("android-p", mapOf("com.whatsapp" to "WhatsApp"), at)
        assertEquals("android.app_labels", observation.kind)
        assertEquals("android:android-p:apps", observation.sourceInstanceKey)
        assertEquals("WhatsApp", JSONObject(observation.payloadJson).getJSONObject("labels").getString("com.whatsapp"))
        assertEquals("labels:${at.toEpochMilli()}", observation.sourceRecordKey)
    }

    @Test
    fun whatWasSentSurvivesSavingAndReading() {
        val labels = mapOf("com.whatsapp" to "WhatsApp", "com.twitter.android" to "X")
        assertEquals(labels, AppLabels.decode(AppLabels.encode(labels)))
        assertTrue(AppLabels.decode("not json").isEmpty())
        assertTrue(AppLabels.decode(null).isEmpty())
    }
}
