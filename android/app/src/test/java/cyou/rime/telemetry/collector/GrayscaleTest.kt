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
}
