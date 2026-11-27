package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant

class OwnerSessionTest {
    private var clock = Instant.parse("2026-11-27T08:00:00Z")
    private val trades = ArrayDeque<OwnerSession.Trade>()
    private val session = OwnerSession({ trades.removeFirstOrNull() ?: OwnerSession.Trade.Unreachable }, { clock })

    private fun opened(cookie: String, hours: Long = 12) = OwnerSession.Trade.Opened(cookie, clock.plusSeconds(hours * 3600))
    private fun answer(status: Int, body: String = "{}") = RawAnswer(status, "application/json", body.toByteArray())

    @Test
    fun aSessionIsTradedOnceAndKept() {
        trades += opened("telemetry_device_session=a")
        val sent = mutableListOf<String>()
        repeat(3) { session.call { cookie -> sent += cookie; answer(200) } }
        assertEquals(listOf("telemetry_device_session=a", "telemetry_device_session=a", "telemetry_device_session=a"), sent)
        assertEquals(1, session.trades)
    }

    @Test
    fun itIsRenewedAnHourBeforeItEnds() {
        trades += opened("telemetry_device_session=a")
        trades += opened("telemetry_device_session=b")
        session.call { answer(200) }
        clock = clock.plusSeconds(10 * 3600 + 59 * 60)
        var used = ""
        session.call { cookie -> used = cookie; answer(200) }
        assertEquals("telemetry_device_session=a", used)
        clock = clock.plusSeconds(60)
        session.call { cookie -> used = cookie; answer(200) }
        assertEquals("telemetry_device_session=b", used)
    }

    @Test
    fun aFailedRenewalKeepsTheOldSessionWhileItIsValid() {
        trades += opened("telemetry_device_session=a")
        session.call { answer(200) }
        clock = clock.plusSeconds(11 * 3600 + 30 * 60)
        var used = ""
        // The trade can't reach the server: the old session still has half an hour.
        session.call { cookie -> used = cookie; answer(200) }
        assertEquals("telemetry_device_session=a", used)
        clock = clock.plusSeconds(3600)
        assertNull(session.call { answer(200) })
    }

    @Test
    fun aRefusedSessionIsTradedAgainAndSentOnceMore() {
        trades += opened("telemetry_device_session=old")
        trades += opened("telemetry_device_session=new")
        val sent = mutableListOf<String>()
        val result = session.call { cookie -> sent += cookie; if (cookie.endsWith("old")) answer(401) else answer(200, "{\"ok\":true}") }
        assertEquals(200, result!!.status)
        assertEquals(listOf("telemetry_device_session=old", "telemetry_device_session=new"), sent)
        assertEquals(2, session.trades)
    }

    @Test
    fun aRefusedTradeGivesTheServersAnswer() {
        trades += OwnerSession.Trade.Refused(answer(404, """{"error":{"code":"unavailable","detail":"device sessions are not configured"}}"""))
        val result = session.call { answer(200) }
        assertEquals(404, result!!.status)
        assertEquals("device sessions are not configured", result.message())
    }

    @Test
    fun theServerIsUnreachable() {
        assertNull(session.call { answer(200) })
        trades += opened("telemetry_device_session=a")
        assertNull(session.call { null })
    }

    @Test
    fun theTradeAnswerIsRead() {
        val opened = OwnerSession.parse("""{"cookie":"telemetry_device_session=abc.123.sig","expires_at":"2026-11-27T20:00:00Z"}""")!!
        assertEquals("telemetry_device_session=abc.123.sig", opened.cookie)
        assertEquals(Instant.parse("2026-11-27T20:00:00Z"), opened.expiresAt)
        assertNull(OwnerSession.parse("""{"cookie":""}"""))
        assertNull(OwnerSession.parse("not json"))
        assertEquals("The server refused it (it answered 500).", RawAnswer.refusalMessage(500, ""))
    }
}
