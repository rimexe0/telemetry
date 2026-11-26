package cyou.rime.telemetry.collector

import android.content.ComponentName
import android.content.Context
import android.media.MediaMetadata
import android.media.session.MediaController
import android.media.session.MediaSessionManager
import android.os.Handler
import android.os.Looper
import org.json.JSONObject
import java.time.Instant

/**
 * What is playing, from Android's media sessions (2026-11-26): the title,
 * the artist or channel, the app and whether it plays. It is read through the
 * notification listener access the phone already has, and recorded only when
 * it changes, so a long video is one observation.
 */
object NowPlaying {
    data class Item(val pkg: String, val title: String, val artist: String?, val album: String?, val state: String)

    /**
     * One session as recorded, or null when it says nothing worth keeping.
     * [playbackState] is Android's PlaybackState state; buffering and seeking
     * count as playing, so a stall is not a change.
     */
    fun item(pkg: String, title: String?, artist: String?, albumArtist: String?, subtitle: String?, album: String?, playbackState: Int?): Item? {
        val name = title?.trim()?.ifBlank { null } ?: return null
        val by = listOf(artist, albumArtist, subtitle).firstNotNullOfOrNull { it?.trim()?.ifBlank { null } }
        val state = when (playbackState) {
            // STATE_PLAYING, FAST_FORWARDING, REWINDING, BUFFERING, CONNECTING, SKIPPING_TO_*.
            3, 4, 5, 6, 8, 9, 10, 11 -> "playing"
            2 -> "paused"
            else -> "stopped"
        }
        return Item(pkg, name, by, album?.trim()?.ifBlank { null }, state)
    }

    /**
     * What changed between the last recorded sessions and the current ones,
     * one per app: new or changed items, and an "ended" item for each app
     * whose session went away.
     */
    fun changes(previous: Map<String, Item>, current: List<Item>): List<Item> {
        val now = current.associateBy { it.pkg }
        val changed = now.values.filter { previous[it.pkg] != it }
        val ended = previous.values.filter { it.pkg !in now && it.state != "ended" }.map { it.copy(state = "ended") }
        return changed + ended
    }

    fun observation(context: Context, item: Item, at: Instant): LocalObservation {
        val deviceId = DeviceIdentity.get(context)
        val payload = JSONObject()
            .put("package", item.pkg)
            .put("title", item.title)
            .put("artist", item.artist ?: JSONObject.NULL)
            .put("album", item.album ?: JSONObject.NULL)
            .put("state", item.state)
        return LocalObservation(
            sourceInstanceKey = "android:$deviceId:media",
            sourceKind = "android_media",
            deviceId = deviceId,
            sourceRecordKey = "media:${item.pkg}:${at.toEpochMilli()}",
            kind = "android.media.now_playing",
            recordType = "now_playing",
            occurredAt = at.toString(),
            endedAt = null,
            collectedAt = at.toString(),
            sourceSchemaVersion = "android-media-session-v1",
            payloadJson = payload.toString(),
        )
    }
}

/**
 * Follows the active media sessions while the notification listener is
 * connected. Changes are gathered for a few seconds before one look, so a
 * burst of callbacks records once. Everything fails quietly: without the
 * access, or on a phone that refuses, nothing is recorded.
 */
class NowPlayingWatch(private val context: Context, private val listener: ComponentName) {
    private val handler = Handler(Looper.getMainLooper())
    private val manager = context.getSystemService(MediaSessionManager::class.java)
    private val controllers = mutableMapOf<android.media.session.MediaSession.Token, Pair<MediaController, MediaController.Callback>>()
    private var recorded: Map<String, NowPlaying.Item> = emptyMap()

    private val sessions = MediaSessionManager.OnActiveSessionsChangedListener { follow(it.orEmpty()) }
    private val look = Runnable { look() }

    fun start() {
        runCatching {
            manager.addOnActiveSessionsChangedListener(sessions, listener, handler)
            follow(manager.getActiveSessions(listener))
        }
    }

    fun stop() {
        handler.removeCallbacks(look)
        runCatching { manager.removeOnActiveSessionsChangedListener(sessions) }
        controllers.values.forEach { (controller, callback) -> runCatching { controller.unregisterCallback(callback) } }
        controllers.clear()
    }

    private fun follow(active: List<MediaController>) {
        val tokens = active.map { it.sessionToken }.toSet()
        controllers.keys.filter { it !in tokens }.forEach { token ->
            controllers.remove(token)?.let { (controller, callback) -> runCatching { controller.unregisterCallback(callback) } }
        }
        active.filter { it.sessionToken !in controllers }.forEach { controller ->
            val callback = object : MediaController.Callback() {
                override fun onMetadataChanged(metadata: MediaMetadata?) = soon()
                override fun onPlaybackStateChanged(state: android.media.session.PlaybackState?) = soon()
                override fun onSessionDestroyed() = soon()
            }
            runCatching { controller.registerCallback(callback, handler) }.onSuccess { controllers[controller.sessionToken] = controller to callback }
        }
        soon()
    }

    private fun soon() {
        handler.removeCallbacks(look)
        handler.postDelayed(look, 3_000L)
    }

    private fun look() {
        val current = controllers.values.mapNotNull { (controller, _) ->
            runCatching {
                val metadata = controller.metadata
                NowPlaying.item(
                    pkg = controller.packageName,
                    title = metadata?.getString(MediaMetadata.METADATA_KEY_TITLE) ?: metadata?.getString(MediaMetadata.METADATA_KEY_DISPLAY_TITLE),
                    artist = metadata?.getString(MediaMetadata.METADATA_KEY_ARTIST),
                    albumArtist = metadata?.getString(MediaMetadata.METADATA_KEY_ALBUM_ARTIST),
                    subtitle = metadata?.getString(MediaMetadata.METADATA_KEY_DISPLAY_SUBTITLE),
                    album = metadata?.getString(MediaMetadata.METADATA_KEY_ALBUM),
                    playbackState = controller.playbackState?.state,
                )
            }.getOrNull()
        }
        val changes = NowPlaying.changes(recorded, current)
        if (changes.isEmpty()) return
        recorded = current.associateBy { it.pkg }
        val app = context.applicationContext
        Thread {
            val at = Instant.now()
            runCatching { HealthQueue(app).enqueue(changes.map { NowPlaying.observation(app, it, at) }) }
        }.start()
    }
}
