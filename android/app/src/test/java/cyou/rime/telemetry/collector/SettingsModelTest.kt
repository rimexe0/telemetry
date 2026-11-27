package cyou.rime.telemetry.collector

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.nio.file.Files
import java.time.Instant
import java.time.ZoneId

// Made-up settings for tests, not the owner's.
class SettingsModelTest {
    private val zone = ZoneId.of("Europe/Istanbul")
    private fun queueFile() = Files.createTempDirectory("settings").toFile().resolve("queue.json")

    @Test
    fun aChangeIsKeptUntilTheServerCanBeReached() {
        val file = queueFile()
        val queue = SettingsQueue(file)
        queue.add(SettingsChange.BUDGET, JSONObject().put("enabled", true), "Budget and costs")
        assertEquals(listOf(SendOutcome.Unreachable), queue.flush { SendOutcome.Unreachable }.map { it.second })
        // Still there after the app is closed and opened again.
        val again = SettingsQueue(file)
        assertEquals(1, again.entries().size)
        val sent = again.flush { SendOutcome.Sent("Saved.") }
        assertEquals("Saved.", (sent.single().second as SendOutcome.Sent).notice)
        assertTrue(again.entries().isEmpty())
    }

    @Test
    fun changesGoInOrderAndWaitBehindOneThatCantBeSent() {
        val queue = SettingsQueue(queueFile())
        queue.add(SettingsChange.BUDGET, JSONObject().put("n", 1), "Budget and costs")
        queue.add(SettingsChange.BEDTIME, JSONObject().put("n", 2), "Bedtime")
        val tried = mutableListOf<String>()
        queue.flush { tried += it.kind; SendOutcome.Unreachable }
        assertEquals(listOf(SettingsChange.BUDGET), tried)
        tried.clear()
        queue.flush { tried += it.kind; SendOutcome.Sent("Saved.") }
        assertEquals(listOf(SettingsChange.BUDGET, SettingsChange.BEDTIME), tried)
    }

    @Test
    fun aRefusedChangeIsNotSentAgainAndKeepsTheServersWords() {
        val queue = SettingsQueue(queueFile())
        queue.add(SettingsChange.BUDGET, JSONObject(), "Budget and costs")
        val result = queue.flush { SendOutcome.Refused("blocking settings are locked until 19:00") }
        assertEquals("blocking settings are locked until 19:00", (result.single().second as SendOutcome.Refused).message)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun aNewerChangeOfTheSameKindReplacesTheWaitingOne() {
        val queue = SettingsQueue(queueFile())
        queue.add(SettingsChange.BUDGET, JSONObject().put("base_minutes", 60), "Budget and costs")
        queue.add(SettingsChange.BEDTIME, JSONObject(), "Bedtime")
        queue.add(SettingsChange.BUDGET, JSONObject().put("base_minutes", 30), "Budget and costs")
        val entries = queue.entries()
        assertEquals(listOf(SettingsChange.BEDTIME, SettingsChange.BUDGET), entries.map { it.kind })
        assertEquals(30, entries.last().payload.getInt("base_minutes"))
    }

    private fun budget(vararg pairs: Pair<String, Double>, enabled: Boolean = true) =
        BudgetNumbers(enabled, BudgetFields.all.associate { it.key to 2.0 } + pairs)

    @Test
    fun askingLessIsSeenAsThePanelSeesIt() {
        val now = budget("base_minutes" to 60.0)
        assertFalse(SettingsRules.budgetAsksLess(now, budget("base_minutes" to 30.0)))
        assertTrue(SettingsRules.budgetAsksLess(now, budget("base_minutes" to 90.0)))
        assertTrue(SettingsRules.budgetAsksLess(now, budget("rate_bedtime" to 1.0, "base_minutes" to 60.0)))
        assertTrue(SettingsRules.budgetAsksLess(now, budget(enabled = false)))
        assertFalse(SettingsRules.budgetAsksLess(budget(enabled = false), budget(enabled = false)))
    }

    @Test
    fun aShorterBedtimeAsksLess() {
        val now = Bedtime(true, "23:00", "08:00", 30)
        assertFalse(SettingsRules.bedtimeAsksLess(now, Bedtime(true, "22:30", "08:00", 30)))
        assertTrue(SettingsRules.bedtimeAsksLess(now, Bedtime(true, "23:30", "08:00", 30)))
        assertTrue(SettingsRules.bedtimeAsksLess(now, Bedtime(true, "23:00", "07:00", 30)))
        assertTrue(SettingsRules.bedtimeAsksLess(now, Bedtime(true, "23:00", "08:00", 10)))
        assertTrue(SettingsRules.bedtimeAsksLess(now, Bedtime(false, "23:00", "08:00", 30)))
        assertFalse(SettingsRules.bedtimeAsksLess(Bedtime(false, "", "", 0), Bedtime(false, "", "", 0)))
        assertEquals(9.0, SettingsRules.bedtimeHours("23:00", "08:00"), 0.001)
    }

    @Test
    fun eachRowShowsItsValue() {
        val policy = SettingsRules.policy("""{"policy":{"id":3,"config":{"timezone":"Europe/Istanbul","closed_by_default":true,
            "distraction_times":[{"start":"19:00","end":"22:00"}],
            "distractions":[{"id":"social","name":"Social","android_packages":["com.twitter.android"],"domains":["x.com","www.reddit.com"]}],
            "budget":{"enabled":true,"base_minutes":60},"budget_next":{"enabled":true,"base_minutes":90},"budget_next_from":"2026-11-28T05:00:00Z"}}}""")!!
        val lock = SettingsRules.lock("""{"lock":{"enabled":true,"windows":[{"start":"00:00","end":"19:00"}],"unlock_minutes":15},
            "bedtime":{"enabled":true,"start":"23:30","end":"07:30"},"state":{"locked":true,"until":"2026-11-27T16:00:00Z","pin_set":true,"pin_open":false,"tries_left":3},
            "extension":{"enabled":true,"quiet_seconds":60}}""")!!
        val labels = SettingsRules.labels("""{"labels":[{"kind":"site","match":"reddit.com","label":"distraction"},{"kind":"app","match":"com.whatsapp","label":"chat"}]}""")!!
        val before = Instant.parse("2026-11-27T10:00:00Z")
        assertEquals("1 h a day", SettingsRules.budgetValue(policy, before))
        assertEquals("1 h 30 min a day", SettingsRules.budgetValue(policy, Instant.parse("2026-11-28T06:00:00Z")))
        assertEquals("19:00-22:00 · bed 23:30", SettingsRules.timesValue(policy, lock))
        assertEquals("3 apps and sites", SettingsRules.distractionsValue(policy, labels))
        assertEquals("Locked until 19:00", SettingsRules.lockValue(lock, zone))
        assertEquals("28 Nov, 08:00", SettingsRules.waitStart("2026-11-28T05:00:00Z", zone))
        assertEquals("", SettingsRules.budgetValue(null, before))
    }
}
