package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

class GrayscaleTest {
    @Test
    fun turnsGrayOnlyWhenAskedAndUndoesOnlyItsOwn() {
        assertEquals(Grayscale.Step.ON, Grayscale.step(wanted = true, owned = false, screenGray = false))
        assertEquals(Grayscale.Step.OFF, Grayscale.step(wanted = false, owned = true, screenGray = true))
        // The owner's own grayscale is left on, and never counted as ours.
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = true, owned = false, screenGray = true))
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = false, owned = false, screenGray = true))
        // Turned off by hand meanwhile: nothing left to undo.
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = false, owned = true, screenGray = false))
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = true, owned = true, screenGray = true))
    }

    @Test
    fun withTheBudgetOffItComesBackAsBefore() {
        assertEquals(Grayscale.Step.ON, Grayscale.step(wanted = true, owned = true, screenGray = false))
    }

    @Test
    fun withTheBudgetOnSwitchingItOffAsksInstead() {
        assertEquals(Grayscale.Step.ASK, Grayscale.step(wanted = true, owned = true, screenGray = false, ask = true))
        // The first switch-on is not a question.
        assertEquals(Grayscale.Step.ON, Grayscale.step(wanted = true, owned = false, screenGray = false, ask = true))
    }

    @Test
    fun keptOffItStaysOffAndTurningItOnAgainIsReported() {
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = true, owned = true, screenGray = false, declinedHere = true, ask = true))
        assertEquals(Grayscale.Step.RESTORE, Grayscale.step(wanted = true, owned = true, screenGray = true, declinedHere = true, ask = true))
    }

    @Test
    fun aDeclineFromAnotherDeviceStopsTheAskingButIsNotThisPhonesToRestore() {
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = true, owned = true, screenGray = false, declinedElsewhere = true, ask = true))
        assertEquals(Grayscale.Step.NONE, Grayscale.step(wanted = true, owned = true, screenGray = true, declinedElsewhere = true, ask = true))
    }

    @Test
    fun whenTheWindDownEndsItsGrayscaleIsUndone() {
        assertEquals(Grayscale.Step.OFF, Grayscale.step(wanted = false, owned = true, screenGray = true, declinedHere = true, ask = true))
    }
}
