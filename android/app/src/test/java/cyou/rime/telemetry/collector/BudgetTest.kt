package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class BudgetTest {
    private val utc = ZoneId.of("UTC")

    // The example from docs/budget-and-habits.md, with a door and two habits.
    private val json = """
        {"decision":{"mode":"closed"},"budget":{
          "enabled": true,
          "day": "2026-11-26",
          "budget": 150, "spent": 42.5, "left": 107.5,
          "earned": 30, "penalty": 0, "debt": 0,
          "debt_tomorrow": 0, "borrowable": 90,
          "rate": {"total": 3, "base": 3, "grayscale_off": 0, "kind": "day"},
          "in_distraction_time": false,
          "next_distraction_time": "2026-11-26T16:00:00Z",
          "bedtime_at": "2026-11-26T21:00:00Z",
          "wind_down": false,
          "grayscale_declined": false,
          "door": {"id": 7, "state": "waiting", "requested_at": "2026-11-26T10:00:00Z", "opens_at": "2026-11-26T10:10:00Z",
                   "window_end": "2026-11-26T11:10:00Z", "ends_at": "2026-11-26T10:21:00Z", "used_minutes": 3.2, "use_minutes": 10, "borrowing": false},
          "door_available": true,
          "door_unavailable_reason": "",
          "next_door_at": null,
          "habits": [{"id": 1, "name": "Walk", "done": true, "value": 30}, {"id": 2, "name": "Read", "done": false, "value": 30}],
          "reasons": ["Finish the thesis", " "],
          "settings": {"wait_minutes": 10, "window_minutes": 60, "window_use_minutes": 10, "grace_minutes": 1, "borrow_multiplier": 2}
        }}
    """.trimIndent()

    @Test
    fun readsTheContractsBudget() {
        val budget = Budget.parse(json)!!
        assertTrue(budget.enabled)
        assertEquals("2026-11-26", budget.day)
        assertEquals(107.5, budget.left, 0.0)
        assertEquals(BudgetRate(3.0, 3.0, 0.0, "day"), budget.rate)
        assertEquals(Instant.parse("2026-11-26T21:00:00Z"), budget.bedtimeAt)
        assertNull(budget.nextDoorAt)
        val door = budget.door!!
        assertEquals(7L, door.id)
        assertTrue(door.waiting)
        assertEquals(10.0, door.useMinutes!!, 0.0)
        assertEquals(listOf(BudgetHabit(1, "Walk", true, 30.0), BudgetHabit(2, "Read", false, 30.0)), budget.habits)
        // Blank reasons are not shown.
        assertEquals(listOf("Finish the thesis"), budget.reasons)
        assertEquals(2.0, budget.settings.borrowMultiplier, 0.0)
        assertNull(budget.settings.rateGrayscaleOff)
    }

    @Test
    fun noBudgetFromAnOlderServerIsNull() {
        assertNull(Budget.parse("""{"decision":{"mode":"closed"}}"""))
        assertNull(Budget.parse("""{"budget":null}"""))
        assertFalse(Budget.parse("""{"budget":{"enabled":false}}""")!!.enabled)
    }

    @Test
    fun aDoorInsideADistractionTimeHasNoUseLimit() {
        val door = Budget.door(org.json.JSONObject("""{"id":3,"state":"open","use_minutes":null,"borrowing":true,"ends_at":"2026-11-26T20:00:00+00:00"}"""))
        assertNull(door.useMinutes)
        assertTrue(door.open)
        assertEquals(Instant.parse("2026-11-26T20:00:00Z"), door.endsAt)
    }

    @Test
    fun theDoorScreenStatesTheCost() {
        val budget = Budget.parse(json)!!
        assertEquals("3× now.", BudgetText.rate(budget.rate))
        assertEquals("10 min = 30 min of budget. Left today: 1 h 48 min.", BudgetText.cost(budget))
        val borrowing = budget.copy(left = 0.0, borrowable = 90.0)
        assertEquals("Borrowing: 10 min = 1 h of tomorrow's budget. Tomorrow can still lend 1 h 30 min.", BudgetText.cost(borrowing))
    }

    @Test
    fun theRateShowsItsParts() {
        assertEquals("13× now: 10× bedtime + 3× grayscale off.", BudgetText.rate(BudgetRate(13.0, 10.0, 3.0, "bedtime")))
        assertEquals("4× now: 1× distraction time + 3× grayscale off.", BudgetText.rate(BudgetRate(4.0, 1.0, 3.0, "distraction")))
    }

    @Test
    fun habitsShowDoneOrWhatTheyWouldEarn() {
        assertEquals("Walk · done", BudgetText.habit(BudgetHabit(1, "Walk", true, 30.0)))
        assertEquals("Read · +30 min", BudgetText.habit(BudgetHabit(2, "Read", false, 30.0)))
    }

    @Test
    fun bedtimeIsNamedDuringTheWindDownAndDuringBedtime() {
        val budget = Budget.parse(json)!!
        val now = Instant.parse("2026-11-26T20:35:10Z")
        assertNull(BudgetText.bedtime(budget, now, null, utc))
        assertEquals("Bedtime in 25 min", BudgetText.bedtime(budget.copy(windDown = true), now, null, utc))
        val bedtime = budget.copy(rate = BudgetRate(10.0, 10.0, 0.0, "bedtime"))
        assertEquals("It's bedtime (until 08:00).", BudgetText.bedtime(bedtime, now, Instant.parse("2026-11-27T08:00:00Z"), utc))
        assertEquals("It's bedtime.", BudgetText.bedtime(bedtime, now, null, utc))
    }

    @Test
    fun anUnavailableDoorSaysWhenTheNextCanOpen() {
        val budget = Budget.parse(json)!!.copy(doorAvailable = false, doorUnavailableReason = "One door an hour.", nextDoorAt = Instant.parse("2026-11-26T11:10:00Z"))
        assertEquals("One door an hour. The next door can open at 11:10.", BudgetText.unavailable(budget, utc))
        assertEquals("No door can open right now.", BudgetText.unavailable(budget.copy(doorUnavailableReason = "", nextDoorAt = null), utc))
    }

    @Test
    fun theWaitCountsDown() {
        val door = Budget.parse(json)!!.door!!
        assertEquals("Opens in 7:32", BudgetText.wait(door, Instant.parse("2026-11-26T10:02:28Z")))
        assertEquals("Opens in 0:00", BudgetText.wait(door, Instant.parse("2026-11-26T10:12:00Z")))
    }

    @Test
    fun minutesReadAsTheOwnerWouldSayThem() {
        assertEquals("0 min", BudgetText.minutes(-3.0))
        assertEquals("45 min", BudgetText.minutes(45.2))
        assertEquals("1 h", BudgetText.minutes(60.0))
        assertEquals("1 h 40 min", BudgetText.minutes(100.0))
        assertEquals("2.5", BudgetText.number(2.5))
        assertEquals("3", BudgetText.number(3.0))
    }

    @Test
    fun theGrayscaleSurchargeComesFromTheState() {
        val budget = Budget.parse(json)!!
        assertNull(budget.grayscaleSurcharge)
        assertEquals(3.0, budget.copy(rate = BudgetRate(13.0, 10.0, 3.0, "bedtime")).grayscaleSurcharge!!, 0.0)
        assertEquals(4.0, budget.copy(settings = budget.settings.copy(rateGrayscaleOff = 4.0)).grayscaleSurcharge!!, 0.0)
    }

    @Test
    fun aRefusalIsShownInTheServersWords() {
        val refused = Door.outcome(DeviceApi.Answer(409, """{"error":"door_cooling_down","message":"The next door can open at 11:10."}"""))
        assertEquals(Door.Outcome.Refused("The next door can open at 11:10."), refused)
        assertEquals(Door.Outcome.Refused("bedtime_started"), Door.outcome(DeviceApi.Answer(409, """{"error":"bedtime_started"}""")))
        assertEquals(Door.Outcome.Refused("The server refused this (409)."), Door.outcome(DeviceApi.Answer(409, "")))
    }

    @Test
    fun noAnswerOrAServerErrorMeansTheServerCannotBeReached() {
        assertEquals(Door.Outcome.Unreachable, Door.outcome(null))
        assertEquals(Door.Outcome.Unreachable, Door.outcome(DeviceApi.Answer(502, "")))
        val done = Door.outcome(DeviceApi.Answer(200, """{"id":8,"state":"waiting"}""")) as Door.Outcome.Done
        assertEquals(8L, done.body.getLong("id"))
    }
}
