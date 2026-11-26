package cyou.rime.telemetry.collector

import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.security.MessageDigest
import java.time.Duration
import java.time.Instant

/**
 * The panel's own files kept on the phone, so Activity and Settings open
 * without fetching them through the relay each time (owner 2026-11-26: "why
 * loading anything takes 2 million years"). Files under
 * /panel/_app/immutable/ carry their content's hash in their names and are
 * kept for good. The page and its other files are kept for the panel release
 * they came with, which the panel names in /panel/_app/version.json; a new
 * release drops them. Without a known release they are kept an hour. Answers
 * from /v1/ are never kept here: the panel caches those itself.
 */
class PanelFiles(private val dir: File) {
    enum class Kind { IMMUTABLE, RELEASE, NONE }

    class Kept(val contentType: String, val body: ByteArray, val version: String?, val at: Instant)

    companion object {
        const val VERSION_PATH = "/panel/_app/version.json"
        private const val IMMUTABLE = "/panel/_app/immutable/"
        val UNVERSIONED_FOR: Duration = Duration.ofHours(1)
        /** Hashed files not read in this long go when a new release comes. */
        val UNUSED_FOR: Duration = Duration.ofDays(30)

        /** Which kind of file a GET for [path] is, as far as keeping it goes. */
        fun kind(method: String, path: String): Kind = when {
            method != "GET" -> Kind.NONE
            path == VERSION_PATH || path.startsWith("$VERSION_PATH?") -> Kind.NONE
            path.startsWith(IMMUTABLE) && !path.contains("..") -> Kind.IMMUTABLE
            path == "/panel" || path.startsWith("/panel/") || path.startsWith("/panel?") -> Kind.RELEASE
            else -> Kind.NONE
        }

        /** The release named in version.json's body, or null. */
        fun version(body: String): String? = runCatching {
            org.json.JSONObject(body).optString("version").takeIf { it.isNotBlank() }
        }.getOrNull()

        private fun name(path: String): String =
            MessageDigest.getInstance("SHA-256").digest(path.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
    }

    /**
     * The kept file for [path], when it may be used: a hashed one always; the
     * others when kept for [version], or, with no release known, for less
     * than an hour.
     */
    @Synchronized
    fun get(path: String, kind: Kind, version: String?, now: Instant = Instant.now()): Kept? {
        if (kind == Kind.NONE) return null
        val file = File(dir, name(path))
        val kept = read(file) ?: return null
        val usable = when (kind) {
            Kind.IMMUTABLE -> true
            else -> if (version != null) kept.version == version else Duration.between(kept.at, now) < UNVERSIONED_FOR
        }
        if (!usable) return null
        if (kind == Kind.IMMUTABLE) file.setLastModified(now.toEpochMilli())
        return kept
    }

    /** Keeps a file the server gave with status 200. */
    @Synchronized
    fun put(path: String, kind: Kind, version: String?, contentType: String, body: ByteArray, now: Instant = Instant.now()) {
        if (kind == Kind.NONE) return
        runCatching {
            dir.mkdirs()
            val file = File(dir, name(path))
            val temporary = File(dir, "${file.name}.tmp")
            DataOutputStream(temporary.outputStream().buffered()).use { out ->
                out.writeUTF(contentType)
                out.writeUTF(if (kind == Kind.IMMUTABLE) "*" else version ?: "")
                out.writeLong(now.toEpochMilli())
                out.writeInt(body.size)
                out.write(body)
            }
            if (!temporary.renameTo(file)) temporary.delete()
            file.setLastModified(now.toEpochMilli())
        }
    }

    /** A new release: its page and files are fetched again; hashed files not read for a month go. */
    @Synchronized
    fun newRelease(now: Instant = Instant.now()) {
        for (file in dir.listFiles().orEmpty()) {
            val kept = read(file)
            val old = kept == null || kept.version != "*" || Duration.between(Instant.ofEpochMilli(file.lastModified()), now) > UNUSED_FOR
            if (old) file.delete()
        }
    }

    private fun read(file: File): Kept? {
        if (!file.isFile) return null
        return runCatching {
            DataInputStream(file.inputStream().buffered()).use { input ->
                val type = input.readUTF()
                val version = input.readUTF()
                val at = Instant.ofEpochMilli(input.readLong())
                val size = input.readInt()
                require(size in 0..(32 shl 20))
                val body = ByteArray(size)
                input.readFully(body)
                Kept(type, body, version.ifEmpty { null }, at)
            }
        }.getOrNull()
    }
}
