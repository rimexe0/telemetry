package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class NowModelTest {
    @Test
    fun theBudgetModesHaveNames() {
        assertEquals("Opened through the door", NowModel.modeLabel("door"))
        assertEquals("Distraction time used up", NowModel.modeLabel("budget_used"))
        assertEquals("Bedtime", NowModel.modeLabel("bedtime"))
        assertEquals("Plan your day", NowModel.modeLabel("plan_gate"))
        assertEquals("Unknown", NowModel.modeLabel("something new"))
    }

    @Test
    fun ageTextIsReadable() {
        val base = Instant.parse("2026-09-28T07:00:00Z")
        assertEquals("just now", NowModel.ageText(base, base.plusSeconds(20)))
        assertEquals("5 min ago", NowModel.ageText(base, base.plusSeconds(300)))
        assertEquals("2 h ago", NowModel.ageText(base, base.plusSeconds(7300)))
    }
}
