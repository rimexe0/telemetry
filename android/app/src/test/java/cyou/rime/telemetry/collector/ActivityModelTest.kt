package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId

// Made-up days for tests, not the owner's.
class ActivityModelTest {
    private val zone = ZoneId.of("Europe/Istanbul")

    private val day = """{"date":"2026-11-20","timezone":"Europe/Istanbul","from":"2026-11-19T21:00:00Z","to":"2026-11-20T09:00:00Z","day":{
        "distraction_minutes":20,"distraction_by_device":[{"device":"phone","minutes":20}],
        "chunks":[
          {"device":"phone","start":"2026-11-20T06:00:00Z","end":"2026-11-20T06:30:00Z","kind":"distraction","active_minutes":25,"distraction_minutes":20,
           "items":[{"name":"com.twitter.android","app_name":"X","label":"distraction","minutes":8,"visits":3},{"name":"com.whatsapp","app_name":"WhatsApp","label":"","minutes":12,"visits":2}]},
          {"device":"mac","start":"2026-11-20T05:00:00Z","end":"2026-11-20T06:10:00Z","kind":"task","task":"Write","active_minutes":60,"distraction_minutes":0,
           "items":[{"name":"T3 Code","label":"work","minutes":60,"visits":1}]},
          {"device":"mac","start":"2026-11-20T07:00:00Z","end":"2026-11-20T07:20:00Z","kind":"other","active_minutes":15,"distraction_minutes":0,"items":[]}
        ],
        "no_data":[{"device":"mac","start":"2026-11-20T06:10:00Z","end":"2026-11-20T07:00:00Z"},{"device":"watch","start":"2026-11-19T21:00:00Z","end":"2026-11-20T09:00:00Z"}]}}"""

    @Test
    fun theTimelineShowsEachDevicesStretchesAndItsMissingDataInOrder() {
        val view = DayModel.parse(day)
        assertNotNull(view)
        val devices = DayModel.timeline(view!!)
        assertEquals(listOf("mac", "phone", "watch"), devices.map { it.device })
        val mac = devices[0]
        assertEquals(listOf("08:00–09:10", "09:10–10:00", "10:00–10:20"), mac.rows.map { DayModel.span(it, zone) })
        assertTrue(mac.rows[1] is TimelineRow.NoData)
        assertEquals(50.0, mac.noDataMinutes, 0.001)
        assertEquals(75.0, mac.activeMinutes, 0.001)
        // A device that sent nothing at all is one stretch with no data, not an empty day.
        val watch = devices[2]
        assertEquals(1, watch.rows.size)
        assertTrue(watch.rows.single() is TimelineRow.NoData)
    }

    @Test
    fun appsAreNamedAsThePhoneNamesThemBiggestFirst() {
        val phone = DayModel.timeline(DayModel.parse(day)!!).first { it.device == "phone" }
        val use = phone.rows.single() as TimelineRow.Use
        assertTrue(use.distraction)
        assertEquals("WhatsApp 12 min · X 8 min", DayModel.itemsLine(use.items))
        assertEquals(20.0, phone.distractionMinutes, 0.001)
    }

    @Test
    fun minutesAreWrittenAsThePanelWritesThem() {
        assertEquals("under 1 min", ActivityText.minutes(0.4))
        assertEquals("0 min", ActivityText.minutes(0.0))
        assertEquals("41 min", ActivityText.minutes(41.2))
        assertEquals("1 h 12 min", ActivityText.minutes(72.0))
        assertEquals("2 h", ActivityText.minutes(120.0))
    }

    @Test
    fun theSummaryNamesPackagesInInsights() {
        val json = """{"insights":[{"key":"distractions","text":"1h on distractions today","detail":"Mostly com.twitter.android: 40m in 3 visits.","names":["com.twitter.android"]}],
            "app_names":{"com.twitter.android":"X"},
            "devices":[{"device":"phone","active_minutes":300,"no_data_minutes":60,"labels":[{"name":"distraction","minutes":60}],
              "apps":[{"name":"com.whatsapp","app_name":"WhatsApp","label":"","minutes":40}],"sites":null}]}"""
        val summary = SummaryModel.parse(json)!!
        assertEquals("Mostly X: 40m in 3 visits.", summary.insights.single().detail)
        val phone = summary.devices.single()
        assertEquals("WhatsApp", phone.apps.single().name)
        assertEquals("5 h active · no data for 1 h", SummaryModel.deviceNote(phone))
        assertEquals("5 h active", SummaryModel.deviceNote(phone.copy(noDataMinutes = 10.0)))
    }
}
