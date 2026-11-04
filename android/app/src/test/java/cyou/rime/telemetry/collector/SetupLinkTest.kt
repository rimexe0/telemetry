package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SetupLinkTest {
    private val key = "BDQAImuIn7FQicgxZZT4ekXWleG0QJlUflNjYQnBUTU7m39PHHlg0TP3d08OeB1xffb1WIrv05kvkL38Zwa8mZk="
    private val encodedKey = java.net.URLEncoder.encode(key, "UTF-8")

    @Test
    fun readsTheAddresses() {
        assertEquals(
            SetupLink.Addresses("https://server.test:8443", "https://relay.test", key),
            SetupLink.parse("telemetry://setup?server=https%3A%2F%2Fserver.test%3A8443%2F&relay=https%3A%2F%2Frelay.test&key=$encodedKey"),
        )
    }

    @Test
    fun aRelayIsOptional() {
        assertEquals(SetupLink.Addresses("https://server.test", "", ""), SetupLink.parse("telemetry://setup?server=https%3A%2F%2Fserver.test"))
    }

    @Test
    fun refusesAnythingElse() {
        assertNull(SetupLink.parse(null))
        assertNull(SetupLink.parse("https://setup?server=https%3A%2F%2Fserver.test"))
        assertNull(SetupLink.parse("telemetry://other?server=https%3A%2F%2Fserver.test"))
        assertNull(SetupLink.parse("telemetry://setup?server=http%3A%2F%2Fserver.test"))
        assertNull(SetupLink.parse("telemetry://setup?server=https%3A%2F%2Fserver.test&relay=http%3A%2F%2Frelay.test&key=$encodedKey"))
        assertNull(SetupLink.parse("telemetry://setup?server=https%3A%2F%2Fserver.test&relay=https%3A%2F%2Frelay.test&key=bad"))
    }
}
