package cyou.rime.telemetry.collector

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class HabitEditsTest {
    /** Stands in for the phone's preferences: a string that outlives each queue. */
    private class Memory : TickStore {
        var json: String? = null
        override fun load(): String? = json
        override fun save(json: String) { this.json = json }
    }

    private fun created(id: Long, name: String) = EditSend.Sent(JSONObject().put("habit", JSONObject().put("id", id).put("name", name).put("weekdays", org.json.JSONArray())))

    @Test
    fun editsAreSentInTheOrderTheyWereMade() {
        val queue = EditQueue(Memory())
        queue.change(3, name = "read")
        queue.change(3, weekdays = listOf(1, 3, 5))
        queue.reasons(listOf("to sleep"))
        val sent = mutableListOf<Triple<String, String, String>>()
        val flushed = queue.flush({ sent += it.request(); EditSend.Sent(null) })
        assertEquals(listOf("PATCH", "PATCH", "PUT"), sent.map { it.first })
        assertEquals("/v1/device/habits/3", sent[0].second)
        assertEquals("read", JSONObject(sent[0].third).getString("name"))
        assertEquals("[1,3,5]", JSONObject(sent[1].third).getJSONArray("weekdays").toString())
        assertEquals("/v1/device/reasons", sent[2].second)
        assertEquals(3, flushed.sent)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun anEditThatMustWaitIsKeptWithEverythingAfterIt() {
        val store = Memory()
        val queue = EditQueue(store)
        queue.change(1, archived = true)
        queue.change(2, name = "walk")
        var calls = 0
        val flushed = queue.flush({ if (calls++ == 0) EditSend.Retry else EditSend.Sent(null) })
        assertEquals(1, calls)
        assertEquals(2, flushed.waiting)
        // A new queue on the same store (the app closed and opened) still has both.
        assertEquals(listOf(1L, 2L), EditQueue(store).entries().map { it.habitId })
    }

    @Test
    fun aRefusedEditIsDroppedAndItsMessageKept() {
        val queue = EditQueue(Memory())
        queue.change(1, name = "")
        queue.change(2, name = "walk")
        val flushed = queue.flush({ if (it.habitId == 1L) EditSend.Refused("a habit name is 1 to 100 characters") else EditSend.Sent(null) })
        assertEquals(listOf("a habit name is 1 to 100 characters"), flushed.refusals)
        assertEquals(1, flushed.sent)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun aNewHabitsLaterEditsReachTheIdTheServerGave() {
        val queue = EditQueue(Memory())
        val added = queue.addHabit("stretch")
        assertTrue(added.habitId < 0)
        queue.change(added.habitId, weekdays = listOf(2))
        val paths = mutableListOf<String>()
        queue.flush({ edit ->
            paths += edit.request().second
            if (edit.kind == HabitEdit.ADD) {
                // An edit made while the new habit was on its way.
                queue.change(added.habitId, name = "stretch more")
                created(41, "stretch")
            } else EditSend.Sent(null)
        })
        assertEquals(listOf("/v1/device/habits", "/v1/device/habits/41", "/v1/device/habits/41"), paths)
        // Made after the server's id was known: it goes to that id at once.
        assertEquals(41L, queue.change(added.habitId, archived = true).habitId)
    }

    @Test
    fun editsToANewHabitTheServerRefusedAreDropped() {
        val queue = EditQueue(Memory())
        val added = queue.addHabit(" ")
        queue.change(added.habitId, name = "x")
        val paths = mutableListOf<String>()
        queue.flush({ paths += it.request().second; EditSend.Refused("a habit name is 1 to 100 characters") })
        assertEquals(listOf("/v1/device/habits"), paths)
        assertTrue(queue.entries().isEmpty())
    }

    @Test
    fun onlyTheNewestReasonsWait() {
        val queue = EditQueue(Memory())
        queue.reasons(listOf("a"))
        queue.change(1, name = "walk")
        queue.reasons(listOf("a", "b"))
        val entries = queue.entries()
        assertEquals(listOf(HabitEdit.CHANGE, HabitEdit.REASONS), entries.map { it.kind })
        assertEquals(listOf("a", "b"), entries.last().reasons)
    }

    @Test
    fun theScreenShowsWaitingEditsOnTopOfTheSavedList() {
        val saved = HabitList(listOf(HabitDef(1, "walk", 0, emptySet(), false), HabitDef(2, "read", 1, setOf(1), false)), listOf("to sleep"))
        val queue = EditQueue(Memory())
        queue.change(1, name = "walk far")
        queue.change(2, archived = true)
        queue.addHabit("stretch", listOf(1, 3))
        queue.reasons(emptyList())
        val shown = EditQueue.apply(saved, queue.entries())!!
        assertEquals(listOf("walk far", "stretch"), shown.active.map { it.name })
        assertEquals(setOf(1, 3), shown.active.last().weekdays)
        assertTrue(shown.reasons.isEmpty())
        // An undo brings it back.
        queue.change(2, archived = false)
        assertEquals(listOf("walk far", "read", "stretch"), EditQueue.apply(saved, queue.entries())!!.active.map { it.name })
    }

    @Test
    fun allSevenDaysOrNoneIsEveryDay() {
        assertEquals(emptyList<Int>(), EditQueue.normalize(setOf(0, 1, 2, 3, 4, 5, 6)))
        assertEquals(emptyList<Int>(), EditQueue.normalize(emptySet()))
        assertEquals(listOf(1, 3, 5), EditQueue.normalize(setOf(5, 1, 3)))
        assertEquals("every day", cyou.rime.telemetry.collector.ui.weekdaysLabel(emptySet()))
        assertEquals("Mon Wed Sun", cyou.rime.telemetry.collector.ui.weekdaysLabel(setOf(0, 3, 1)))
    }

    @Test
    fun theServersAnswerIsTakenAsTicksAre() {
        assertEquals(EditSend.Retry, EditQueue.outcome(null))
        assertEquals(EditSend.Retry, EditQueue.outcome(DeviceApi.Answer(503, "")))
        assertEquals(EditSend.Retry, EditQueue.outcome(DeviceApi.Answer(401, "")))
        assertTrue(EditQueue.outcome(DeviceApi.Answer(400, """{"error":"invalid_habit","message":"no"}""")) is EditSend.Refused)
        assertTrue(EditQueue.outcome(DeviceApi.Answer(201, """{"habit":{"id":1}}""")) is EditSend.Sent)
    }

    @Test
    fun anAcceptedEditGoesIntoTheSavedList() {
        val list = """{"habits":[{"id":1,"name":"walk","position":0,"weekdays":[0,1,2,3,4,5,6]},{"id":2,"name":"read","position":1,"weekdays":[1]}],"reasons":["a"]}"""
        val renamed = HabitCache.patchList(list, HabitEdit("k", HabitEdit.CHANGE, 1, name = "walk far"),
            JSONObject("""{"habit":{"id":1,"name":"walk far","position":0,"weekdays":[]}}"""))!!
        assertEquals(listOf("walk far", "read"), HabitJson.list(renamed)!!.active.map { it.name })
        val archived = HabitCache.patchList(renamed, HabitEdit("k", HabitEdit.CHANGE, 2, archived = true),
            JSONObject("""{"habit":{"id":2,"name":"read","position":1,"weekdays":[1],"archived_at":"2026-11-26T20:00:00Z"}}"""))!!
        assertEquals(listOf("walk far"), HabitJson.list(archived)!!.active.map { it.name })
        val added = HabitCache.patchList(archived, HabitEdit("k", HabitEdit.ADD, -1, name = "stretch"),
            JSONObject("""{"habit":{"id":7,"name":"stretch","position":2,"weekdays":[]}}"""))!!
        assertEquals(listOf("walk far", "stretch"), HabitJson.list(added)!!.active.map { it.name })
        val reasons = HabitCache.patchList(added, HabitEdit("k", HabitEdit.REASONS, reasons = listOf("b")), JSONObject("""{"reasons":["b"]}"""))!!
        assertEquals(listOf("b"), HabitJson.list(reasons)!!.reasons)
        assertNull(HabitCache.patchList(list, HabitEdit("k", HabitEdit.CHANGE, 1, name = "x"), null))
    }
}
