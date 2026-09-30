package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

class IncidentWatchTest {
    @Test
    fun keepsOtherDevicesIncidentsOnly() {
        val json = """{"collection_incidents":[
            {"id":7,"device":"mac","kind":"silent","started_at":"2026-09-30T10:00:00Z","detail":{"message":"Mac quiet"}},
            {"id":8,"device":"phone","kind":"silent","started_at":"2026-09-30T10:00:00Z","detail":{}}]}"""
        val incidents = IncidentWatch.parse(json)
        assertEquals(listOf(7L), incidents.map { it.id })
        assertEquals("Mac quiet", incidents.single().message)
    }

    @Test
    fun namesTheDeviceFromTheIncident() {
        val json = """{"collection_incidents":[
            {"id":9,"device":"datalix","kind":"module_failing:t3","started_at":"2026-09-30T10:00:00Z","detail":{"device_name":"vps1","message":"x"}},
            {"id":10,"device":"mac-00000001","kind":"silent","started_at":"2026-09-30T10:00:00Z","detail":{"device_name":"plagiarism.local"}}]}"""
        val incidents = IncidentWatch.parse(json)
        assertEquals(true, IncidentWatch.title(incidents[0]).startsWith("vps1: t3 collection failing since"))
        assertEquals(true, IncidentWatch.title(incidents[1]).startsWith("plagiarism: no data since"))
    }

    @Test
    fun missingFieldMeansNoIncidents() {
        assertEquals(emptyList<CollectionIncident>(), IncidentWatch.parse("""{"decision":{}}"""))
    }
}
