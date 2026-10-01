package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Test

class AppSuspenderTest {
    private val work = BlockDecision(
        mode = "work", reason = "Write the collector.", policyVersion = 1, blockAll = false,
        blocked = setOf("com.twitter.android", "com.whatsapp", "com.android.settings", "gone.app"),
        alwaysAllowed = setOf("com.whatsapp"),
    )

    @Test
    fun suspendsOnlyExplicitInstalledBlockedApps() {
        val wanted = AppSuspender.wanted(work, { it != "gone.app" }, setOf("com.android.settings"), "cyou.rime.telemetry.collector")
        assertEquals(setOf("com.twitter.android"), wanted)
        val gate = work.copy(blockAll = true, blocked = emptySet())
        assertEquals("gates are left to the block screen", emptySet<String>(), AppSuspender.wanted(gate, { true }, emptySet(), "x"))
        assertEquals(emptySet<String>(), AppSuspender.wanted(null, { true }, emptySet(), "x"))
    }

    @Test
    fun plansOnlyTheDifferenceUnlessTheReasonChanged() {
        val plan = AppSuspender.plan(setOf("a", "b"), setOf("b", "c"), "Write.", "Write.")
        assertEquals(setOf("a"), plan.suspend)
        assertEquals(setOf("c"), plan.unsuspend)
        val newReason = AppSuspender.plan(setOf("a", "b"), setOf("a", "b"), "Rest.", "Write.")
        assertEquals(setOf("a", "b"), newReason.suspend)
        assertEquals(emptySet<String>(), AppSuspender.plan(emptySet(), emptySet(), "", null).suspend)
    }

    @Test
    fun quotesForTheShell() {
        assertEquals("'It'\\''s work time'", AppSuspender.quote("It's work time"))
    }
}
