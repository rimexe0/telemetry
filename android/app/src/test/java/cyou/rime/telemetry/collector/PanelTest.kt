package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class PanelTest {
    private val server = "https://server.test:8443"

    @Test
    fun theAppLinksOpenTheNativeScreens() {
        assertEquals(PanelRoute.Settings(SettingsSection.PERMISSIONS), PanelLinks.route("telemetry-app://settings/permissions", server))
        assertEquals(PanelRoute.Settings(SettingsSection.NOTIFICATIONS), PanelLinks.route("telemetry-app://settings/notifications", server))
        assertEquals(PanelRoute.Settings(null), PanelLinks.route("telemetry-app://settings", server))
        assertEquals(PanelRoute.Setup(null), PanelLinks.route("telemetry-app://setup", server))
        assertEquals(PanelRoute.Setup(SetupStep.USAGE_ACCESS), PanelLinks.route("telemetry-app://setup?step=usage_access", server))
        assertEquals(PanelRoute.Setup(null), PanelLinks.route("telemetry-app://setup?step=nonsense", server))
    }

    @Test
    fun unknownAppLinksAndOtherSchemesAreNeverFollowed() {
        assertEquals(PanelRoute.Refused, PanelLinks.route("telemetry-app://settings/advanced", server))
        assertEquals(PanelRoute.Refused, PanelLinks.route("telemetry-app://pair", server))
        assertEquals(PanelRoute.Refused, PanelLinks.route("telemetry-app://setup/now", server))
        for (link in listOf("intent://scan/#Intent;scheme=zxing;end", "javascript:alert(1)", "file:///sdcard/a", "content://x/y",
            "tel:123", "telemetry://setup?server=https%3A%2F%2Fevil.test", "data:text/html,hi", "not a link")) {
            assertEquals(link, PanelRoute.Refused, PanelLinks.route(link, server))
        }
    }

    @Test
    fun theServerStaysInTheAppAndOtherSitesOpenInTheBrowser() {
        assertEquals(PanelRoute.InPanel, PanelLinks.route("https://server.test:8443/panel/#/habits", server))
        assertEquals(PanelRoute.InPanel, PanelLinks.route("HTTPS://SERVER.test:8443/v1/x", server))
        // Another port or host is another site, where the phone's blocking applies.
        assertEquals(PanelRoute.Browser("https://server.test/panel/"), PanelLinks.route("https://server.test/panel/", server))
        assertEquals(PanelRoute.Browser("https://youtube.com/"), PanelLinks.route("https://youtube.com/", server))
        assertEquals(PanelRoute.InPanel, PanelLinks.route("https://www.youtube.com/embed/x", server, mainFrame = false))
        assertEquals(PanelRoute.InPanel, PanelLinks.route("https://te.rime.cyou/panel/", "https://te.rime.cyou"))
        assertEquals(PanelRoute.InPanel, PanelLinks.route("https://te.rime.cyou:443/panel/", "https://te.rime.cyou"))
    }

    @Test
    fun theUserAgentNamesTheApp() {
        assertEquals("Mozilla/5.0 (Linux; Android 16) Chrome/140 Mobile TelemetryApp/0.1.512 TelemetryAppNav/1",
            PanelLinks.userAgent("Mozilla/5.0 (Linux; Android 16) Chrome/140 Mobile", "0.1.512"))
    }

    @Test
    fun panelAddressesBelongToTheAppsPlaces() {
        assertEquals("activity", PanelLinks.place("#activity"))
        assertEquals("activity", PanelLinks.place("#activity/timeline/2026-11-20"))
        assertEquals("activity", PanelLinks.place("#plan/2026-11-27"))
        assertEquals("settings", PanelLinks.place("#settings/budget"))
        // Editing habits is in the panel, under Settings in the app.
        assertEquals("settings", PanelLinks.place("#habits"))
        assertEquals("today", PanelLinks.place("#today"))
        assertEquals("today", PanelLinks.place(""))
        assertEquals("#settings/sign-in", PanelLinks.safeHash("#settings/sign-in"))
        assertNull(PanelLinks.safeHash("#x');alert(1)//"))
        assertNull(PanelLinks.safeHash("activity"))
    }

    @Test
    fun readsTheSessionAndRenewsItBeforeItEnds() {
        val opened = PanelSession.parse("""{"cookie":"telemetry_device_session=abc.123.sig","expires_at":"2026-11-26T22:00:00.123456789Z"}""")!!
        assertEquals("telemetry_device_session=abc.123.sig", opened.cookie)
        assertEquals(Instant.parse("2026-11-26T22:00:00.123456789Z"), opened.expiresAt)
        assertNull(PanelSession.parse("""{"cookie":""}"""))
        assertNull(PanelSession.parse("not json"))
        assertNull(PanelSession.parse("""{"cookie":"a=b"}""")!!.expiresAt)

        val opens = Instant.parse("2026-11-26T10:00:00Z")
        val ends = Instant.parse("2026-11-26T22:00:00Z")
        assertFalse(PanelSession.renewDue(ends, opens, Instant.parse("2026-11-26T20:59:00Z")))
        assertTrue(PanelSession.renewDue(ends, opens, Instant.parse("2026-11-26T21:00:00Z")))
        assertTrue(PanelSession.renewDue(ends, opens, Instant.parse("2026-11-27T08:00:00Z")))
        // Without an end, an hour after it was opened.
        assertFalse(PanelSession.renewDue(null, opens, Instant.parse("2026-11-26T10:59:00Z")))
        assertTrue(PanelSession.renewDue(null, opens, Instant.parse("2026-11-26T11:00:00Z")))
    }
}
