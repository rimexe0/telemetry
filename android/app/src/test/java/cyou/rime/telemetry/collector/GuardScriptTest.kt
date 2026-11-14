package cyou.rime.telemetry.collector

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class GuardScriptTest {
    private val script = GuardScript.build(
        dir = "/data/user/0/cyou.rime.telemetry.collector/files/guard",
        component = "cyou.rime.telemetry.collector/.TelemetryAccessibilityService",
        packages = setOf("com.example.feed", "com.example.video"),
        message = "Closed during Work. Don't drift.",
    )

    @Test
    fun putsTheBlockScreenBack() {
        assertTrue(script.contains("component='cyou.rime.telemetry.collector/.TelemetryAccessibilityService'"))
        assertTrue(script.contains("settings put secure enabled_accessibility_services"))
        assertTrue(script.contains("settings put secure accessibility_enabled 1"))
        assertTrue(script.contains("note block_screen_off"))
    }

    @Test
    fun putsTheGrayOutsBackForEachBlockedApp() {
        assertTrue(script.contains("for p in com.example.feed com.example.video; do"))
        assertTrue(script.contains("pm suspend --dialogMessage"))
        assertTrue(script.contains("\$p"))
    }

    @Test
    fun quotesTheReasonSoTheShellReadsItWhole() {
        assertTrue(script.contains("'\\''"))
    }

    @Test
    fun withoutGrayOutsItOnlyEnforcesTheBlockScreen() {
        val plain = GuardScript.build("/tmp/guard", "a/b", emptySet(), "")
        assertFalse(plain.contains("pm suspend"))
        assertTrue(plain.contains("while [ ! -f \"\$dir/stop\" ]; do"))
    }

    @Test
    fun theEmergencyPauseIsCheckedBeforeAnyChange() {
        assertTrue(script.contains("if [ -f \"\$dir/pause\" ]; then"))
        assertTrue(script.indexOf("pause") < script.indexOf("enabled_accessibility_services"))
    }
}
