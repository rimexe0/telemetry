package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class NowPlayingTest {
    @Test
    fun aSessionNamesWhatPlaysAndWhoMadeIt() {
        assertEquals(NowPlaying.Item("com.example.music", "Song", "Band", "Album", "playing"),
            NowPlaying.item("com.example.music", " Song ", "Band", null, null, "Album", 3))
        // A video app may give the channel only as the subtitle.
        assertEquals("Channel", NowPlaying.item("com.example.video", "Video", null, " ", "Channel", null, 2)?.artist)
        assertEquals("paused", NowPlaying.item("com.example.video", "Video", null, null, null, null, 2)?.state)
        // Buffering is not a change from playing.
        assertEquals("playing", NowPlaying.item("com.example.video", "Video", null, null, null, null, 6)?.state)
        assertEquals("stopped", NowPlaying.item("com.example.video", "Video", null, null, null, null, null)?.state)
    }

    @Test
    fun aSessionWithoutATitleIsNotRecorded() {
        assertNull(NowPlaying.item("com.example.music", "  ", "Band", null, null, null, 3))
        assertNull(NowPlaying.item("com.example.music", null, "Band", null, null, null, 3))
    }

    @Test
    fun onlyChangesAreRecorded() {
        val song = NowPlaying.Item("com.example.music", "Song", "Band", null, "playing")
        val video = NowPlaying.Item("com.example.video", "Video", "Channel", null, "playing")
        assertEquals(listOf(song), NowPlaying.changes(emptyMap(), listOf(song)))
        assertEquals(emptyList<NowPlaying.Item>(), NowPlaying.changes(mapOf(song.pkg to song), listOf(song)))
        val paused = song.copy(state = "paused")
        assertEquals(listOf(paused), NowPlaying.changes(mapOf(song.pkg to song), listOf(paused)))
        // A session that went away is recorded as ended, once.
        assertEquals(listOf(video, song.copy(state = "ended")), NowPlaying.changes(mapOf(song.pkg to song), listOf(video)))
    }
}
