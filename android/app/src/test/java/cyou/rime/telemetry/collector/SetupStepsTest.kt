package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SetupStepsTest {
    @Test
    fun stepsComeInOrder() {
        assertEquals(SetupStep.PAIR, SetupStep.next(emptySet(), emptySet()))
        assertEquals(SetupStep.NOTIFICATIONS, SetupStep.next(setOf(SetupStep.PAIR), emptySet()))
    }

    @Test
    fun skippedOptionalStepsAreNotAskedAgain() {
        val done = setOf(SetupStep.PAIR, SetupStep.NOTIFICATIONS, SetupStep.USAGE_ACCESS)
        assertEquals(SetupStep.HEALTH, SetupStep.next(done, emptySet()))
        assertEquals(SetupStep.SAMSUNG_HEALTH, SetupStep.next(done, setOf(SetupStep.HEALTH)))
        assertEquals(SetupStep.LOCATION, SetupStep.next(done, setOf(SetupStep.HEALTH, SetupStep.SAMSUNG_HEALTH)))
    }

    @Test
    fun backgroundLocationWaitsForLocation() {
        val done = setOf(SetupStep.PAIR, SetupStep.NOTIFICATIONS, SetupStep.USAGE_ACCESS, SetupStep.HEALTH, SetupStep.SAMSUNG_HEALTH)
        assertEquals(SetupStep.CALENDAR, SetupStep.next(done, setOf(SetupStep.LOCATION)))
        assertEquals(SetupStep.BACKGROUND_LOCATION, SetupStep.next(done + SetupStep.LOCATION, emptySet()))
    }

    @Test
    fun finishedWhenEverythingIsDoneOrSkipped() {
        val all = SetupStep.entries.toSet()
        assertNull(SetupStep.next(all, emptySet()))
        assertEquals(0, SetupStep.remainingRequired(all))
        assertEquals(4, SetupStep.remainingRequired(emptySet()))
    }

    @Test
    fun missingPermissionsStayNamedEvenWhenSkipped() {
        val done = setOf(SetupStep.PAIR, SetupStep.NOTIFICATIONS, SetupStep.USAGE_ACCESS, SetupStep.START, SetupStep.BATTERY, SetupStep.CALENDAR, SetupStep.SAMSUNG_HEALTH)
        // Health and location are extras; blocking and notification access matter for blocking.
        assertEquals(listOf(SetupStep.BLOCKING, SetupStep.NOTIFICATION_ACCESS), SetupStep.missing(done))
        assertEquals(emptyList<SetupStep>(), SetupStep.missing(SetupStep.entries.toSet()))
    }
}
