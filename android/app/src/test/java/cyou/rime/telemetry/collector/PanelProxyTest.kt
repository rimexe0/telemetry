package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class PanelProxyTest {
    @Test
    fun onlyTheWebViewsKeyIsLetIn() {
        assertTrue(PanelProxy.keyed("a=1; telemetry_app=k3y; b=2", "k3y"))
        assertFalse(PanelProxy.keyed("telemetry_app=other", "k3y"))
        assertFalse(PanelProxy.keyed("telemetry_app_x=k3y", "k3y"))
        assertFalse(PanelProxy.keyed(null, "k3y"))
        assertFalse(PanelProxy.keyed("", "k3y"))
    }

    @Test
    fun theSessionComesFromTheTradesAnswer() {
        val (cookie, expires) = PanelProxy.parseSession("""{"cookie":"telemetry_device_session=a.b.c","expires_at":"2026-11-27T10:00:00Z"}""")!!
        assertEquals("telemetry_device_session=a.b.c", cookie)
        assertEquals(Instant.parse("2026-11-27T10:00:00Z"), expires)
        assertNull(PanelProxy.parseSession("""{"cookie":""}"""))
        assertNull(PanelProxy.parseSession("not json"))
    }
}
