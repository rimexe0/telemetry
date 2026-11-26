package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

class CounterTest {
    private val t0 = Instant.parse("2026-11-26T10:00:00Z")
    private val utc = ZoneId.of("UTC")

    private fun at(minutes: Long, seconds: Long = 0) = t0.plusSeconds(minutes * 60 + seconds)

    @Test
    fun aSittingGoesOnAcrossShortSwitches() {
        val sitting = Sitting()
        sitting.enter(at(0))
        assertTrue(sitting.inFront)
        sitting.leave(at(5))
        assertFalse(sitting.inFront)
        // Back within two minutes: the same sitting.
        sitting.enter(at(6))
        assertEquals(Duration.ofMinutes(8), sitting.used(at(9)))
        // Entering twice changes nothing.
        sitting.enter(at(9))
        assertEquals(Duration.ofMinutes(8), sitting.used(at(9)))
    }

    @Test
    fun leavingLongerStartsANewSitting() {
        val sitting = Sitting()
        sitting.enter(at(0))
        sitting.leave(at(5))
        sitting.enter(at(10))
        assertEquals(Duration.ofMinutes(1), sitting.used(at(11)))
    }

    @Test
    fun useSinceTheFetchCountsOnlyTimeInFront() {
        val sitting = Sitting()
        sitting.enter(at(0))
        sitting.leave(at(4))
        sitting.enter(at(5))
        assertEquals(Duration.ofMinutes(4), sitting.usedSince(at(2), at(7)))
        assertEquals(Duration.ZERO, Sitting().usedSince(at(2), at(7)))
    }

    private val budget = BudgetView(
        enabled = true, day = "2026-11-26", budget = 150.0, spent = 42.5, left = 100.0, earned = 30.0, penalty = 0.0, debt = 0.0,
        debtTomorrow = 0.0, borrowable = 90.0, rate = BudgetRate(3.0, 3.0, 0.0, "day"), inDistractionTime = false,
        nextDistractionTime = null, bedtimeAt = Instant.parse("2026-11-26T21:00:00Z"), windDown = false, grayscaleDeclined = false,
        door = null, doorAvailable = true, doorUnavailableReason = "", nextDoorAt = null, habits = emptyList(), reasons = emptyList(),
        settings = BudgetSettings(10.0, 60.0, 10.0, 1.0, 2.0, null),
    )

    private fun door(state: String, used: Double, use: Double?, endsAt: Instant? = null) =
        BudgetDoor(7, state, null, null, null, endsAt, used, use, false)

    @Test
    fun theCounterShowsTheSittingTheRateAndWhatIsLeft() {
        // Two minutes in front since the fetch at 3× leaves 6 minutes less.
        val lines = CounterText.lines(budget, Duration.ofMinutes(12), Duration.ofMinutes(2), at(0), utc)
        assertEquals(listOf("12 min · 3×", "1 h 34 min left"), lines)
        assertEquals("Borrowing from tomorrow", CounterText.lines(budget.copy(left = 3.0), Duration.ofMinutes(1), Duration.ofMinutes(1), at(0), utc)[1])
    }

    @Test
    fun inADoorWindowItShowsTheWindowsUse() {
        val open = budget.copy(door = door("open", 5.2, 10.0))
        assertEquals("6 of 10 min", CounterText.lines(open, Duration.ofMinutes(6), Duration.ofSeconds(50), at(0), utc)[1])
        // Never past the allowance.
        assertEquals("10 of 10 min", CounterText.lines(open, Duration.ofMinutes(6), Duration.ofMinutes(9), at(0), utc)[1])
        // Inside a distraction time there is no window limit: the budget is shown.
        val borrowing = budget.copy(door = door("open", 5.0, null))
        assertEquals("1 h 40 min left", CounterText.lines(borrowing, Duration.ZERO, Duration.ZERO, at(0), utc)[1])
    }

    @Test
    fun inGraceItSaysTimeToCloseOrSave() {
        val grace = budget.copy(door = door("grace", 10.0, 10.0, endsAt = at(0, 40)))
        assertEquals("1 min to close or save", CounterText.lines(grace, Duration.ofMinutes(10), Duration.ZERO, at(0), utc)[1])
    }

    @Test
    fun duringTheWindDownItAddsBedtime() {
        val windDown = budget.copy(windDown = true, bedtimeAt = at(25))
        assertEquals("Bedtime in 25 min", CounterText.lines(windDown, Duration.ZERO, Duration.ZERO, at(0), utc).last())
    }
}
