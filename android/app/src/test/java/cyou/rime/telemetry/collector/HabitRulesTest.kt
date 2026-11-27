package cyou.rime.telemetry.collector

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.LocalTime
import java.time.ZoneId

class HabitRulesTest {
    private val zone = ZoneId.of("Europe/Istanbul")
    private val day = "2026-11-26"

    /** A made-up place for tests, not the owner's. */
    private val rule = HabitRule(name = "Test pier", lat = 41.0, lon = 29.0, radiusM = 100, from = HabitRule.WAKE, until = "11:00", minutes = 3)

    private fun local(hour: Int, minute: Int, second: Int = 0): Instant =
        java.time.LocalDateTime.of(2026, 11, 26, hour, minute, second).atZone(zone).toInstant()

    /** A position about [meters] north of the test place. */
    private fun fix(hour: Int, minute: Int, meters: Double, accuracy: Double = 10.0) =
        RuleFix(local(hour, minute), rule.lat + meters / 111_195, rule.lon, accuracy)

    private val from = local(8, 0)
    private val until = local(11, 0)

    @Test
    fun theRuleIsMetOnceTheMinutesInsideAddUp() {
        val walk = listOf(fix(8, 40, 400.0), fix(8, 44, 60.0), fix(8, 45, 20.0), fix(8, 46, 10.0), fix(8, 47, 30.0), fix(8, 50, 400.0))
        assertEquals(local(8, 47), HabitRuleEval.reached(rule, walk, from, until))
        // Two minutes inside is not three.
        assertNull(HabitRuleEval.reached(rule, walk.take(4), from, until))
    }

    @Test
    fun outsideTheRadiusOrTheWindowNothingCounts() {
        assertNull(HabitRuleEval.reached(rule, listOf(fix(9, 0, 150.0), fix(9, 5, 140.0), fix(9, 10, 130.0)), from, until))
        assertNull(HabitRuleEval.reached(rule, listOf(fix(7, 0, 10.0), fix(7, 30, 10.0), fix(7, 50, 10.0)), from, until))
        assertNull(HabitRuleEval.reached(rule, listOf(fix(11, 5, 10.0), fix(11, 10, 10.0)), from, until))
        // A stay across the start counts from the start.
        assertEquals(local(8, 3), HabitRuleEval.reached(rule, listOf(fix(7, 55, 10.0), fix(8, 5, 10.0)), from, until))
        // A long gap is missing data, a poor position doesn't count, and no positions are nothing.
        assertNull(HabitRuleEval.reached(rule, listOf(fix(9, 0, 10.0), fix(9, 40, 10.0)), from, until))
        assertNull(HabitRuleEval.reached(rule, listOf(fix(9, 0, 10.0), fix(9, 5, 10.0, accuracy = 300.0)), from, until))
        assertNull(HabitRuleEval.reached(rule, emptyList(), from, until))
    }

    @Test
    fun theWindowFromWakingOpensAtTheDayStartOnThePhoneAndTheServersWins() {
        val start = LocalTime.of(8, 0)
        val local = HabitRuleEval.window(rule, day, zone, start, closeAt = null, server = null)!!
        assertEquals(local(8, 0), local.from)
        assertEquals(local(11, 0), local.until)
        assertFalse(local.wakeKnown)
        val fixed = HabitRuleEval.window(rule.copy(from = "09:30"), day, zone, start, null, null)!!
        assertEquals(local(9, 30), fixed.from)
        // Bedtime closes the day's habits before the window would end.
        assertEquals(local(10, 0), HabitRuleEval.window(rule, day, zone, start, local(10, 0), null)!!.until)
        // The server knows the wake.
        val server = RuleWindow(local(9, 12), local(11, 0), wakeKnown = true)
        assertEquals(server, HabitRuleEval.window(rule, day, zone, start, null, server))
        // Nothing left of it.
        assertNull(HabitRuleEval.window(rule.copy(from = "12:00", until = "11:00"), day, zone, LocalTime.of(4, 0), local(11, 30), null))
    }

    @Test
    fun theOwnersWordWins() {
        assertTrue(HabitRuleEval.mayTick(done = false, unticked = false, queuedToday = false, firedToday = false))
        assertFalse(HabitRuleEval.mayTick(done = true, unticked = false, queuedToday = false, firedToday = false))
        assertFalse(HabitRuleEval.mayTick(done = false, unticked = true, queuedToday = false, firedToday = false))
        assertFalse(HabitRuleEval.mayTick(done = false, unticked = false, queuedToday = true, firedToday = false))
        assertFalse(HabitRuleEval.mayTick(done = false, unticked = false, queuedToday = false, firedToday = true))
    }

    @Test
    fun aRuleTickUntickedOnThePhoneStaysUnticked() {
        val budget = Budget.fromObject(JSONObject().put("day", day).put("habits", org.json.JSONArray().put(
            JSONObject().put("id", 1).put("name", "Going outside").put("done", false).put("value", 30).put("rule", rule.toJson()),
        )))
        val queue = TickQueue(object : TickStore {
            var json: String? = null
            override fun load() = json
            override fun save(json: String) { this.json = json }
        })
        queue.add(1, day, local(8, 47), done = true, byRule = true)
        var habit = TodayHabits.list(day, budget, null, queue.entries())!!.single()
        assertTrue(habit.done)
        assertEquals("rule", habit.doneBy)
        assertEquals(local(8, 47), habit.doneAt)
        assertEquals("reached 08:47", RuleText.line(rule, habit.done, habit.doneBy, habit.doneAt, null, local(9, 0), zone))
        queue.add(1, day, local(9, 0), done = false)
        habit = TodayHabits.list(day, budget, null, queue.entries())!!.single()
        assertFalse(habit.done)
        assertTrue(habit.unticked)
        assertFalse(HabitRuleEval.mayTick(habit.done, habit.unticked, queuedToday = true, firedToday = false))
        // The server saying it was unticked is enough on its own.
        val served = Budget.fromObject(JSONObject().put("day", day).put("habits", org.json.JSONArray().put(
            JSONObject().put("id", 1).put("name", "Going outside").put("done", false).put("unticked", true).put("rule", rule.toJson()),
        )))
        val fromServer = TodayHabits.list(day, served, null, emptyList())!!.single()
        assertFalse(HabitRuleEval.mayTick(fromServer.done, fromServer.unticked, queuedToday = false, firedToday = false))
    }

    @Test
    fun aRuleTickIsSentAsTheRulesAndItsRefusalIsNotShown() {
        val queue = TickQueue(object : TickStore {
            var json: String? = null
            override fun load() = json
            override fun save(json: String) { this.json = json }
        })
        queue.add(1, day, local(8, 47), done = true, byRule = true)
        val saved = TickQueue.decode(TickQueue.encode(queue.entries())).single()
        assertTrue(saved.byRule)
        val flushed = queue.flush { TickSend.Refused("Bedtime had already started then.") }
        assertTrue(flushed.refusals.isEmpty())
    }

    @Test
    fun theDistanceText() {
        assertEquals("40 m", RuleText.distance(38.0))
        assertEquals("420 m", RuleText.distance(418.0))
        assertEquals("1.2 km", RuleText.distance(1234.0))
        assertEquals("12 km", RuleText.distance(12_345.0))
        val now = local(9, 0)
        assertEquals("Test pier · 420 m away", RuleText.line(rule, false, null, null, fix(8, 55, 420.0), now, zone))
        assertEquals("Test pier · location unknown", RuleText.line(rule, false, null, null, null, now, zone))
        // A poor or old position is not where the phone is.
        assertEquals("Test pier · location unknown", RuleText.line(rule, false, null, null, fix(8, 55, 420.0, accuracy = 500.0), now, zone))
        assertEquals("Test pier · location unknown", RuleText.line(rule, false, null, null, RuleFix(now.minusSeconds(13 * 3600), rule.lat, rule.lon, 5.0), now, zone))
        // Ticked by hand: nothing to add.
        assertNull(RuleText.line(rule, true, "hand", local(8, 30), null, now, zone))
        assertEquals("Going outside after waking up: Test pier is 420 m away.", RuleText.notice("Going outside after waking up", rule, fix(8, 55, 420.0), now))
        assertEquals("Going outside after waking up: go to Test pier.", RuleText.notice("Going outside after waking up", rule, null, now))
        assertEquals("Test pier · 100 m · from waking until 11:00 · 3 min", RuleText.summary(rule))
        assertEquals("No rule", RuleText.summary(null))
    }

    @Test
    fun theMorningNoticeIsSaidOnceADay() {
        val window = RuleWindow(local(8, 20), local(11, 0), wakeKnown = true)
        val now = local(8, 25)
        // Not before the window opens, nor while the screen is off.
        assertFalse(RuleNotice.due(window, local(8, 10), done = false, unticked = false, interactive = true, day = day, said = null))
        assertFalse(RuleNotice.due(window, now, done = false, unticked = false, interactive = false, day = day, said = null))
        assertTrue(RuleNotice.due(window, now, done = false, unticked = false, interactive = true, day = day, said = null))
        // Said today: never again today.
        assertFalse(RuleNotice.due(window, local(9, 30), done = false, unticked = false, interactive = true, day = day, said = day))
        // Tomorrow it may be said again.
        val tomorrow = RuleWindow(window.from.plusSeconds(86_400), window.until.plusSeconds(86_400), true)
        assertTrue(RuleNotice.due(tomorrow, now.plusSeconds(86_400), done = false, unticked = false, interactive = true, day = "2026-11-27", said = day))
        // Not once it is done or the owner unticked it, nor after the window.
        assertFalse(RuleNotice.due(window, now, done = true, unticked = false, interactive = true, day = day, said = null))
        assertFalse(RuleNotice.due(window, now, done = false, unticked = true, interactive = true, day = day, said = null))
        assertFalse(RuleNotice.due(window, local(11, 0), done = false, unticked = false, interactive = true, day = day, said = null))
        assertFalse(RuleNotice.due(null, now, done = false, unticked = false, interactive = true, day = day, said = null))
    }

    @Test
    fun theRuleTravelsAsTheServersJson() {
        val json = rule.toJson()
        assertEquals("place", json.getString("kind"))
        assertEquals(rule, HabitRule.parse(JSONObject(json.toString())))
        assertNull(HabitRule.of(JSONObject().put("rule", JSONObject.NULL)))
        assertNull(HabitRule.parse(JSONObject().put("kind", "steps")))
        val habit = HabitJson.list(JSONObject().put("habits", org.json.JSONArray().put(
            JSONObject().put("id", 1).put("name", "Going outside").put("rule", json))).toString())!!.habits.single()
        assertEquals(rule, habit.rule)
        val set = HabitEdit("k", HabitEdit.CHANGE, 1, rule = rule).request()
        assertEquals("PATCH", set.first)
        assertEquals("place", JSONObject(set.third).getJSONObject("rule").getString("kind"))
        val cleared = JSONObject(HabitEdit("k", HabitEdit.CHANGE, 1, clearRule = true).request().third)
        assertTrue(cleared.has("rule") && cleared.isNull("rule"))
        // A rule edit waiting to be sent shows at once.
        val list = HabitList(listOf(HabitDef(1, "Going outside", 0, emptySet(), false)), emptyList())
        assertEquals(rule, EditQueue.apply(list, listOf(HabitEdit("k", HabitEdit.CHANGE, 1, rule = rule)))!!.habits.single().rule)
        assertNull(EditQueue.apply(list.copy(habits = list.habits.map { it.copy(rule = rule) }), listOf(HabitEdit("k", HabitEdit.CHANGE, 1, clearRule = true)))!!.habits.single().rule)
    }

    @Test
    fun thePickersFieldsMakeARuleOrSayWhatIsMissing() {
        assertEquals("Pick the place on the map or by name.", RuleDraft(until = "12:00").rule().second)
        val picked = RuleDraft(name = "Test pier", lat = 41.0, lon = 29.0, until = "12:00")
        assertEquals(HabitRule("Test pier", 41.0, 29.0, 100, HabitRule.WAKE, "12:00", 1), picked.rule().first)
        assertEquals("Until is a time such as 11:00.", picked.copy(until = "noon").rule().second)
        assertEquals("The distance is 20 to 2000 m.", picked.copy(radius = "5").rule().second)
        assertEquals("From is a time such as 07:30.", picked.copy(fromWake = false, from = "7").rule().second)
        assertEquals(rule.copy(minutes = 3), RuleDraft.of(rule).rule().first)
    }

    @Test
    fun placeSearchReadsNominatim() {
        val body = """[{"lat":"41.0001","lon":"29.0002","name":"Test pier","display_name":"Test pier, Some street, Some town, Country"}]"""
        val found = PlaceSearch.parse(body).single()
        assertEquals("Test pier", found.name)
        assertEquals("Some street, Some town", found.detail)
        assertEquals(41.0001, found.lat, 1e-9)
        assertTrue(PlaceSearch.parse("not json").isEmpty())
        val url = PlaceSearch.url("test pier", RuleFix(Instant.EPOCH, 41.0, 29.0, 5.0))
        assertTrue(url.startsWith("https://nominatim.openstreetmap.org/search?"))
        assertTrue(url.contains("q=test+pier") && url.contains("viewbox="))
        assertNotNull(PlaceSearch.USER_AGENT)
        assertFalse(PlaceSearch.USER_AGENT.contains("@"))
    }

    @Test
    fun positionsAreKeptAsCompactJson() {
        val fixes = listOf(fix(8, 0, 10.0), fix(8, 1, 20.0))
        assertEquals(fixes, HabitRuleWatch.decode(HabitRuleWatch.encode(fixes)))
        assertTrue(HabitRuleWatch.decode("nonsense").isEmpty())
    }
}
