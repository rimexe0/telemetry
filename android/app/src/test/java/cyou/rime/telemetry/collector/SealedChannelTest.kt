package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assume.assumeTrue
import org.junit.Test

class SealedChannelTest {
    private fun hex(value: String) = value.chunked(2).map { it.toInt(16).toByte() }.toByteArray()

    @Test
    fun hkdfMatchesRfc5869() {
        // RFC 5869 test case 1.
        val okm = SealedChannel.hkdf(
            ByteArray(22) { 0x0b },
            hex("000102030405060708090a0b0c"),
            hex("f0f1f2f3f4f5f6f7f8f9"),
            42,
        )
        assertEquals(
            "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
            okm.joinToString("") { "%02x".format(it) },
        )
    }

    @Test
    fun sealedRequestHidesThePathAndToken() {
        val channel = SealedChannel("https://relay.invalid", "BDQAImuIn7FQicgxZZT4ekXWleG0QJlUflNjYQnBUTU7m39PHHlg0TP3d08OeB1xffb1WIrv05kvkL38Zwa8mZk=")
        val (message, _) = channel.seal("""{"path":"/v1/device/now","headers":{"Authorization":"Bearer secret-token"}}""".toByteArray(), ByteArray(0))
        val text = String(message, Charsets.ISO_8859_1)
        assertFalse(text.contains("secret-token"))
        assertFalse(text.contains("/v1/device"))
        assertEquals(1.toByte(), message[0])
        assertEquals(4.toByte(), message[1])
    }

    /** Talks to the real server through the tunnel; run with TELEMETRY_RELAY_LIVE=1. */
    @Test
    fun liveServerOpensThePhonesSealAndAnswers() {
        assumeTrue(System.getenv("TELEMETRY_RELAY_LIVE") == "1")
        val channel = SealedChannel(BuildConfig.TELEMETRY_RELAY_URL, BuildConfig.TELEMETRY_RELAY_KEY)
        val answer = channel.exchange("GET", "/v1/device/now", mapOf("Accept" to "application/json"), null)
        assertEquals(401, answer.status)
        assertEquals(404, channel.exchange("GET", "/mcp", emptyMap(), null).status)
    }
}
