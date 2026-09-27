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

    @Test
    fun toleratesADeviceClockSlightlyBehindTheServer() {
        assertEquals("work", BlockPolicy.at(json, Instant.parse("2026-09-28T05:59:57Z"))?.mode)
        assertNull(BlockPolicy.at(json, Instant.parse("2026-09-28T05:40:00Z")))
    }
}
