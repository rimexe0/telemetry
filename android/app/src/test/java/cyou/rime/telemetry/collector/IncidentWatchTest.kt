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
    fun missingFieldMeansNoIncidents() {
        assertEquals(emptyList<CollectionIncident>(), IncidentWatch.parse("""{"decision":{}}"""))
    }
}
