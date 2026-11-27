package cyou.rime.telemetry.collector

import android.content.Context
import android.graphics.drawable.Drawable
import org.osmdroid.tileprovider.MapTileProviderArray
import org.osmdroid.tileprovider.modules.MapTileModuleProviderBase
import org.osmdroid.tileprovider.tilesource.ITileSource
import org.osmdroid.tileprovider.tilesource.XYTileSource
import org.osmdroid.util.MapTileIndex
import java.io.ByteArrayInputStream
import java.io.File

/**
 * Map tiles for Activity › Places, fetched through the owner's server
 * (/v1/places/tiles/{z}/{x}/{y}.png) with this phone's panel session, so
 * OpenStreetMap never sees the phone. Tiles are kept on the phone, so a
 * place seen once draws again without the server. Nothing is ever fetched
 * from OpenStreetMap directly.
 */
class ServerTiles(context: Context) : MapTileModuleProviderBase(4, 40) {
    companion object {
        /** The source's name; tiles are drawn by it and cached under it, never downloaded by osmdroid. */
        val SOURCE: XYTileSource = XYTileSource("TelemetryServer", 0, 19, 256, ".png", arrayOf(""), "© OpenStreetMap")
        private const val KEEP_BYTES = 40L * 1024 * 1024
        private const val TRIM_TO_BYTES = 30L * 1024 * 1024

        /** A tile provider for a map view that reads only through [ServerTiles]. */
        fun provider(context: Context): MapTileProviderArray =
            MapTileProviderArray(SOURCE, null, arrayOf(ServerTiles(context.applicationContext)))

        fun path(index: Long): String =
            "/v1/places/tiles/${MapTileIndex.getZoom(index)}/${MapTileIndex.getX(index)}/${MapTileIndex.getY(index)}.png"
    }

    private val app = context.applicationContext
    private val dir = File(app.cacheDir, "server-tiles")
    @Volatile private var source: ITileSource = SOURCE
    private var written = 0

    override fun getName(): String = "Telemetry server tiles"
    override fun getThreadGroupName(): String = "server-tiles"
    override fun getUsesDataConnection(): Boolean = true
    override fun getMinimumZoomLevel(): Int = 0
    override fun getMaximumZoomLevel(): Int = 19
    override fun setTileSource(tileSource: ITileSource) {
        source = tileSource
    }

    private fun file(index: Long) = File(dir, "${MapTileIndex.getZoom(index)}-${MapTileIndex.getX(index)}-${MapTileIndex.getY(index)}.png")

    override fun getTileLoader(): TileLoader = object : TileLoader() {
        override fun loadTile(pMapTileIndex: Long): Drawable? {
            val kept = file(pMapTileIndex)
            if (kept.isFile) {
                runCatching { kept.setLastModified(System.currentTimeMillis()) }
                runCatching { return source.getDrawable(kept.absolutePath) }
            }
            val answer = OwnerApi.send(app, "GET", path(pMapTileIndex), accept = "image/png", readTimeoutMillis = 20_000) ?: return null
            if (!answer.ok || !answer.contentType.startsWith("image/") || answer.body.isEmpty()) return null
            val drawable = runCatching { source.getDrawable(ByteArrayInputStream(answer.body)) }.getOrNull() ?: return null
            runCatching { keep(kept, answer.body) }
            return drawable
        }
    }

    @Synchronized
    private fun keep(target: File, body: ByteArray) {
        dir.mkdirs()
        val temp = File(dir, target.name + ".new")
        temp.writeBytes(body)
        temp.renameTo(target)
        if (++written % 50 == 0) trim()
    }

    /** Oldest-used tiles go first once the kept tiles pass 40 MB: the disk is small. */
    private fun trim() {
        val files = dir.listFiles()?.filter { it.isFile } ?: return
        var total = files.sumOf { it.length() }
        if (total <= KEEP_BYTES) return
        for (file in files.sortedBy { it.lastModified() }) {
            if (total <= TRIM_TO_BYTES) break
            total -= file.length()
            file.delete()
        }
    }
}
