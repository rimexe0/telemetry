package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class BlockPolicyTest {
    private val json = """
        {"timeline":[
          {"from":"2026-09-28T06:00:00Z","to":"2026-09-28T08:00:00Z","decision":{"mode":"work","reason":"Working on Client work.","policy_version":3,"block_all":false,
            "blocked":{"android_packages":["com.example.feed","com.example.shared"]},"always_allowed":{"android_packages":["com.example.chat"]}}},
          {"from":"2026-09-28T08:00:00Z","to":"2026-09-28T09:00:00Z","decision":{"mode":"gate","reason":"Go outside.","policy_version":3,"block_all":true,
            "blocked":{"android_packages":[]},"always_allowed":{"android_packages":["com.example.chat"]}}},
          {"from":"2026-09-28T09:00:00Z","to":"2026-09-28T20:00:00Z","decision":{"mode":"leisure","reason":"Free time.","policy_version":3,"block_all":false,
            "blocked":{"android_packages":[]},"always_allowed":{"android_packages":[]}}}]}
    """.trimIndent()
    private val safe = setOf("com.example.launcher")

    @Test
    fun blocksListedAppsDuringWork() {
        val decision = BlockPolicy.at(json, Instant.parse("2026-09-28T07:00:00Z"))
        assertEquals("work", decision?.mode)
        assertEquals(3L, decision?.policyVersion)
        assertTrue(BlockPolicy.shouldBlock(decision, "com.example.feed", safe))
        assertFalse(BlockPolicy.shouldBlock(decision, "com.example.editor", safe))
    }

    @Test
    fun leisureBlocksNothing() {
        val decision = BlockPolicy.at(json, Instant.parse("2026-09-28T10:00:00Z"))
        assertFalse(decision!!.active)
        assertFalse(BlockPolicy.shouldBlock(decision, "com.example.feed", safe))
    }

    @Test
    fun gatesBlockEverythingExceptEssentialsAndSystem() {
        val decision = BlockPolicy.at(json, Instant.parse("2026-09-28T08:30:00Z"))
        assertTrue(BlockPolicy.shouldBlock(decision, "com.example.editor", safe))
        assertFalse(BlockPolicy.shouldBlock(decision, "com.example.chat", safe))
        assertFalse(BlockPolicy.shouldBlock(decision, "com.example.launcher", safe))
    }

    @Test
    fun missingOrOutdatedCacheNeverBlocks() {
        assertNull(BlockPolicy.at(json, Instant.parse("2026-09-28T21:00:00Z")))
        assertFalse(BlockPolicy.shouldBlock(null, "com.example.feed", safe))
    }

    // A day with the budget on: closed, a waiting door that opens at 10:10
    // with its sliding end at 10:21, closed again, the distraction time until
    // the budget runs out, then bedtime.
    private val budgetJson = """
        {"decision":{"mode":"closed","blocked":{"android_packages":["com.example.feed"]}},"timeline":[
          {"from":"2026-11-26T10:00:00Z","to":"2026-11-26T10:10:00Z","decision":{"mode":"closed","reason":"Focus hours.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":["com.example.feed"]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-26T10:10:00Z","to":"2026-11-26T10:21:00Z","decision":{"mode":"door","reason":"Opened through the door until 10:21.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":[]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-26T10:21:00Z","to":"2026-11-26T16:00:00Z","decision":{"mode":"closed","reason":"Focus hours.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":["com.example.feed","com.example.video"]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-26T16:00:00Z","to":"2026-11-26T17:30:00Z","decision":{"mode":"leisure","reason":"Free time.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":[]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-26T17:30:00Z","to":"2026-11-26T21:00:00Z","decision":{"mode":"budget_used","reason":"Today's distraction time is used up.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":["com.example.feed","com.example.video"]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-26T21:00:00Z","to":"2026-11-27T02:00:00Z","decision":{"mode":"bedtime","reason":"Bedtime until 08:00.","policy_version":9,"block_all":true,
            "blocked":{"android_packages":["com.example.feed"]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-27T02:00:00Z","to":"2026-11-27T08:00:00Z","decision":{"mode":"bedtime","reason":"Bedtime until 08:00.","policy_version":9,"block_all":true,
            "blocked":{"android_packages":["com.example.feed"]},"always_allowed":{"android_packages":["com.example.maps"]}}},
          {"from":"2026-11-27T08:00:00Z","to":"2026-11-27T10:00:00Z","decision":{"mode":"closed","reason":"Focus hours.","policy_version":9,"block_all":false,
            "blocked":{"android_packages":["com.example.feed"]},"always_allowed":{"android_packages":["com.example.maps"]}}}]}
    """.trimIndent()

    @Test
    fun anOpenDoorOpensAndItsSlidingEndClosesAgain() {
        assertTrue(BlockPolicy.shouldBlock(BlockPolicy.at(budgetJson, Instant.parse("2026-11-26T10:05:00Z")), "com.example.feed", safe))
        val door = BlockPolicy.at(budgetJson, Instant.parse("2026-11-26T10:15:00Z"))
        assertEquals("door", door?.mode)
        assertFalse(door!!.active)
        assertFalse(BlockPolicy.shouldBlock(door, "com.example.feed", safe))
        // Offline, the phone closes at the last end it received.
        assertTrue(BlockPolicy.shouldBlock(BlockPolicy.at(budgetJson, Instant.parse("2026-11-26T10:21:00Z")), "com.example.feed", safe))
    }

    @Test
    fun aUsedUpDistractionTimeClosesTheDistractionsOnly() {
        val used = BlockPolicy.at(budgetJson, Instant.parse("2026-11-26T18:00:00Z"))
        assertEquals("budget_used", used?.mode)
        assertEquals("Today's distraction time is used up.", used?.reason)
        assertTrue(BlockPolicy.shouldBlock(used, "com.example.video", safe))
        assertFalse(BlockPolicy.shouldBlock(used, "com.example.maps", safe))
        assertFalse(BlockPolicy.shouldBlock(used, "com.example.editor", safe))
    }

    @Test
    fun distractionsAreKnownWhileTheyAreOpen() {
        assertEquals(setOf("com.example.feed", "com.example.video"), BlockPolicy.distractionPackages(budgetJson))
        assertEquals(emptySet<String>(), BlockPolicy.distractionPackages("not json"))
    }

    @Test
    fun bedtimeEndsAtTheEndOfItsRunOfSegments() {
        assertEquals(Instant.parse("2026-11-27T08:00:00Z"), BlockPolicy.bedtimeUntil(budgetJson, Instant.parse("2026-11-26T23:00:00Z")))
        assertNull(BlockPolicy.bedtimeUntil(budgetJson, Instant.parse("2026-11-26T18:00:00Z")))
    }

    @Test
    fun toleratesADeviceClockSlightlyBehindTheServer() {
        assertEquals("work", BlockPolicy.at(json, Instant.parse("2026-09-28T05:59:57Z"))?.mode)
        assertNull(BlockPolicy.at(json, Instant.parse("2026-09-28T05:40:00Z")))
    }
}
