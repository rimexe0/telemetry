package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class PlaceRuleTest {
    private val home = ReminderPlace(arrive = true, name = "home", home = true, points = listOf(PlacePoint(38.4600, 17.2200, 120.0)))
    private val now = Instant.parse("2026-10-10T17:00:00Z")
    private val away = 38.4700 // about 1.1 km north
    private val atHome = 38.4601

    private fun look(place: ReminderPlace, lat: Double?, wifi: Boolean = false, accuracy: Double = 15.0) =
        PlaceRule.where(place, lat, 17.2200, accuracy, wifi)

    @Test
    fun arrivingHomeFiresOnlyAfterBeingAway() {
        // Set while away: arriving fires.
        var seen = PlaceRule.first(home, look(home, away))
        assertFalse(PlaceRule.fires(home, seen, look(home, away), now, now))
        seen = PlaceRule.update(seen, look(home, away))
        assertTrue(PlaceRule.fires(home, seen, look(home, atHome), now, now))

        // Set at home: it waits for the next arrival.
        val atStart = PlaceRule.first(home, look(home, atHome))
        assertFalse(PlaceRule.fires(home, atStart, look(home, atHome), now, now))
        val after = PlaceRule.update(atStart, look(home, away))
        assertTrue(PlaceRule.fires(home, after, look(home, atHome), now, now))
    }

    @Test
    fun homeWifiCountsAsHomeAndGpsWobbleDoesNot() {
        val seen = PlaceRule.first(home, look(home, away))
        // No usable GPS indoors, but on the home Wi-Fi.
        assertEquals(PlaceRule.Where.IN, look(home, null, wifi = true))
        assertTrue(PlaceRule.fires(home, seen, look(home, null, wifi = true), now, now))
        // 150 m out is past the 120 m circle but within the edge: unsure.
        assertEquals(PlaceRule.Where.UNSURE, look(home, 38.4600 + 150 / 111_320.0))
        // A poor fix decides nothing.
        assertEquals(PlaceRule.Where.UNSURE, look(home, away, accuracy = 300.0))
    }

    @Test
    fun leavingFiresOnlyAfterBeingInside() {
        val leave = home.copy(arrive = false)
        val unknown = PlaceRule.first(leave, PlaceRule.Where.UNSURE)
        assertFalse(PlaceRule.fires(leave, unknown, look(leave, away), now, now))
        val inside = PlaceRule.update(unknown, look(leave, atHome))
        assertTrue(PlaceRule.fires(leave, inside, look(leave, away), now, now))
    }

    @Test
    fun anEarliestTimeHoldsItBack() {
        val seen = PlaceRule.first(home, look(home, away))
        assertFalse(PlaceRule.fires(home, seen, look(home, atHome), now, now.plusSeconds(3600)))
    }

    @Test
    fun anyOfSeveralPlacesCounts() {
        val migros = ReminderPlace(true, "Migros", false, listOf(PlacePoint(38.40, 17.10, 60.0), PlacePoint(38.50, 17.30, 60.0)))
        assertEquals(PlaceRule.Where.IN, PlaceRule.where(migros, 38.5002, 17.3001, 10.0, onHomeWifi = true))
        // The home Wi-Fi means nothing for a shop.
        assertEquals(PlaceRule.Where.UNSURE, PlaceRule.where(migros, null, null, null, onHomeWifi = true))
    }

    @Test
    fun readsPlaceFollowUpsAndWaitsForThePlace() {
        val items = FollowUpWatch.parse("""{"follow_ups":[{"id":9,"title":"Tell lil bro about job listings","due_at":"2026-10-10T16:00:00Z","state":"open",
            "place":{"when":"arrive","name":"home","home":true,"points":[{"lat":38.46,"lon":17.22,"radius":120}]}}]}""")
        val item = items.single()
        assertTrue(item.place!!.home)
        assertNull(item.dueSince)
        assertFalse(item.dueBy(now))
        val reached = item.copy(triggeredAt = now)
        assertTrue(reached.dueBy(now))
        assertEquals("Arrived at home 5 min ago", dueText(reached, now.plusSeconds(300)))
    }
}
