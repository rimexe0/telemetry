package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test
import java.nio.file.Files
import java.time.Duration
import java.time.Instant

class PanelFilesTest {
    private val now = Instant.parse("2026-11-26T20:00:00Z")
    private fun files() = PanelFiles(Files.createTempDirectory("panel-files").toFile())

    @Test
    fun onlyThePanelsOwnFilesAreKept() {
        assertEquals(PanelFiles.Kind.IMMUTABLE, PanelFiles.kind("GET", "/panel/_app/immutable/chunks/app.B1x2.js"))
        assertEquals(PanelFiles.Kind.RELEASE, PanelFiles.kind("GET", "/panel/"))
        assertEquals(PanelFiles.Kind.RELEASE, PanelFiles.kind("GET", "/panel/favicon.png"))
        assertEquals(PanelFiles.Kind.NONE, PanelFiles.kind("GET", "/panel/_app/version.json"))
        assertEquals(PanelFiles.Kind.NONE, PanelFiles.kind("GET", "/v1/habits"))
        assertEquals(PanelFiles.Kind.NONE, PanelFiles.kind("POST", "/panel/"))
        val files = files()
        files.put("/v1/habits", PanelFiles.kind("GET", "/v1/habits"), "1", "application/json", "{}".toByteArray(), now)
        assertNull(files.get("/v1/habits", PanelFiles.Kind.RELEASE, "1", now))
    }

    @Test
    fun hashedFilesAreKeptAcrossReleases() {
        val files = files()
        val path = "/panel/_app/immutable/entry/start.js"
        files.put(path, PanelFiles.Kind.IMMUTABLE, "1", "text/javascript", "start".toByteArray(), now)
        files.newRelease(now.plusSeconds(60))
        val kept = files.get(path, PanelFiles.Kind.IMMUTABLE, "2", now.plus(Duration.ofDays(400)))
        assertEquals("start", String(kept!!.body))
        assertEquals("text/javascript", kept.contentType)
    }

    @Test
    fun thePageIsKeptForItsReleaseOnly() {
        val files = files()
        files.put("/panel/", PanelFiles.Kind.RELEASE, "1", "text/html", "<html>".toByteArray(), now)
        assertNotNull(files.get("/panel/", PanelFiles.Kind.RELEASE, "1", now.plus(Duration.ofDays(3))))
        assertNull(files.get("/panel/", PanelFiles.Kind.RELEASE, "2", now))
        files.newRelease(now)
        assertNull(files.get("/panel/", PanelFiles.Kind.RELEASE, "1", now))
    }

    @Test
    fun withoutAReleaseThePageLastsAnHour() {
        val files = files()
        files.put("/panel/", PanelFiles.Kind.RELEASE, null, "text/html", "<html>".toByteArray(), now)
        assertNotNull(files.get("/panel/", PanelFiles.Kind.RELEASE, null, now.plus(Duration.ofMinutes(59))))
        assertNull(files.get("/panel/", PanelFiles.Kind.RELEASE, null, now.plus(Duration.ofMinutes(61))))
    }

    @Test
    fun theReleaseIsReadFromVersionJson() {
        assertEquals("1795721483721", PanelFiles.version("""{"version":"1795721483721"}"""))
        assertNull(PanelFiles.version("not json"))
    }
}
