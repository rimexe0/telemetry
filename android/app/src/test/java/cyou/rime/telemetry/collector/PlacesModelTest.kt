package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

// Made-up places for tests, not the owner's (the panel's places.test.ts).
class PlacesModelTest {
    private val zone = ZoneId.of("Europe/Istanbul")
    private val home = 41.018 to 29.0
    private val pier = 41.0 to 29.0
    private val cafe = 41.03 to 29.02

    private fun at(clock: String): Instant = java.time.OffsetDateTime.parse("2026-11-20T$clock:00+03:00").toInstant()
    private fun fix(clock: String, place: Pair<Double, Double>, accuracy: Double = 10.0) = RuleFix(at(clock), place.first, place.second, accuracy)

    private fun testDay(): PlacesDay {
        val positions = mutableListOf<RuleFix>()
        for (hour in 0..8) positions += fix("%02d:00".format(hour), home)
        positions += listOf(
            fix("08:10", 41.009 to 29.0),
            fix("08:12", 41.2 to 29.3, 400.0),
            fix("08:17", pier),
            fix("08:18", 41.0003 to 29.0),
            fix("08:30", 41.01 to 29.01),
            fix("09:15", cafe), fix("09:30", cafe),
            fix("09:50", home), fix("10:20", home),
            fix("11:05", 41.02 to 29.01), fix("11:10", 41.021 to 29.01),
        )
        return PlacesDay(
            date = "2026-11-20", zone = zone, from = at("00:00"), to = Instant.parse("2026-11-20T21:00:00Z"),
            positions = positions,
            stays = listOf(
                PlaceStay(at("00:00"), at("08:10"), home.first, home.second, true, emptyList()),
                PlaceStay(at("09:15"), at("09:50"), cafe.first, cafe.second, false, listOf(NearbyPlace("Test café", cafe.first, cafe.second, 4.0))),
                PlaceStay(at("09:50"), at("10:20"), home.first, home.second, true, emptyList()),
            ),
            rules = listOf(HabitPlace(1, "Morning walk", "Test pier", pier.first, pier.second, 100)),
        )
    }

    @Test
    fun positionsWorseThan100mAreLeftOut() {
        val points = Places.usable(testDay().positions)
        assertFalse(points.any { it.lat == 41.2 })
        assertEquals(testDay().positions.size - 1, points.size)
    }

    @Test
    fun theLineBreaksWherePositionsAreMoreThan15MinutesApart() {
        val lines = Places.pathLines(Places.usable(listOf(fix("08:00", home), fix("08:15", home), fix("08:31", pier), fix("08:40", pier))))
        assertEquals(listOf(2, 2), lines.map { it.size })
        assertEquals(emptyList<List<RuleFix>>(), Places.pathLines(emptyList()))
    }

    @Test
    fun insideAStayThePathDrawsNoLinesOnlyBetweenStays() {
        val walk = 41.009 to 29.0
        val other = 41.04 to 29.03
        val positions = mutableListOf<RuleFix>()
        // GPS jitter indoors, within 40 m of home and of the café.
        for (minute in 0..56 step 4) positions += fix("08:%02d".format(minute), (home.first + (minute % 3 - 1) * 0.0003) to (home.second + (minute % 2) * 0.0003))
        positions += fix("09:00", home)
        positions += fix("09:05", walk)
        for (minute in listOf(10, 18, 26, 34, 40)) positions += fix("09:$minute", (cafe.first + (minute % 3 - 1) * 0.0002) to cafe.second)
        positions += fix("09:45", other)
        val stays = listOf(
            PlaceStay(at("08:00"), at("09:00"), home.first, home.second, true, emptyList()),
            PlaceStay(at("09:10"), at("09:40"), cafe.first, cafe.second, false, emptyList()),
        )
        val lines = Places.pathLines(Places.usable(positions), stays)
        assertEquals(1, lines.size)
        assertEquals(listOf(home, walk, cafe, other), lines[0].map { it.lat to it.lon })
        // Without stays the jitter is drawn as it came.
        assertEquals(positions.size, Places.pathLines(Places.usable(positions), emptyList()).sumOf { it.size })
    }

    @Test
    fun theGapBreaksStayInsideAndAroundStays() {
        val walk = 41.009 to 29.0
        val stays = listOf(PlaceStay(at("08:00"), at("08:40"), home.first, home.second, true, emptyList()))
        val lines = Places.pathLines(listOf(fix("08:00", home), fix("08:40", 41.0181 to 29.0), fix("08:45", walk), fix("09:30", walk)), stays)
        assertEquals(listOf(listOf(home), listOf(home, walk), listOf(walk)), lines.map { line -> line.map { it.lat to it.lon } })
        // A position at home outside the stay's time is the path's own.
        val after = Places.pathLines(listOf(fix("08:45", walk), fix("08:50", 41.0181 to 29.0)), stays)
        assertEquals(listOf(listOf(walk, 41.0181 to 29.0)), after.map { line -> line.map { it.lat to it.lon } })
    }

    @Test
    fun gapsAreStretchesOver15MinutesWithoutPositionsOutsideStays() {
        val day = testDay()
        val gaps = Places.gaps(Places.usable(day.positions), day.stays.map { Span(it.from, it.to) }, day.from, day.to)
        assertEquals(listOf(Span(at("08:30"), at("09:15")), Span(at("10:20"), at("11:05")), Span(at("11:10"), day.to)), gaps)
        assertEquals(listOf(Span(at("00:00"), at("12:00"))), Places.gaps(emptyList(), emptyList(), at("00:00"), at("12:00")))
    }

    @Test
    fun habitPlacesAreReachedWithinTheirRadius() {
        val day = testDay()
        val visits = Places.visits(Places.usable(day.positions), day.rules)
        assertEquals(1, visits.size)
        assertEquals(at("08:17") to at("08:18"), visits[0].from to visits[0].to)
        assertTrue(Places.visits(Places.usable(listOf(fix("08:17", pier, 150.0))), day.rules).isEmpty())
    }

    @Test
    fun theListHasStaysHabitPlacesAndMissingPositionsInOrder() {
        assertEquals(
            listOf(
                "home 00:00–08:10",
                "08:17 Test pier · 1 min",
                "no positions 08:30–09:15",
                "09:15–09:50 Test café · 35 min",
                "home 09:50–10:20",
                "no positions 10:20–11:05",
                "no positions 11:10–24:00",
            ),
            Places.list(testDay()).map { it.text },
        )
    }

    @Test
    fun todaySaysSinceWhen() {
        assertEquals("no positions since 11:10", Places.list(testDay().copy(to = at("12:00"))).last().text)
    }

    @Test
    fun aStayAtAHabitPlaceIsNamedAfterIt() {
        val day = testDay().let { it.copy(positions = it.positions + fix("09:20", cafe), rules = listOf(HabitPlace(2, "Coffee", "Test corner", cafe.first, cafe.second, 50))) }
        val texts = Places.list(day).map { it.text }
        assertTrue(texts.contains("09:15–09:50 Test corner · 35 min"))
        assertFalse(texts.any { it.startsWith("09:15 Test corner") })
    }

    @Test
    fun aStayWithNoNamedPlaceShowsItsTimes() {
        val day = testDay().let { d -> d.copy(stays = d.stays.mapIndexed { i, s -> if (i == 1) s.copy(nearby = emptyList()) else s }) }
        assertTrue(Places.list(day).any { it.kind == PlaceItem.Kind.STAY && it.text == "09:15–09:50 · 35 min" })
    }

    @Test
    fun aDayWithoutPositionsIsOneGap() {
        val day = testDay().copy(positions = emptyList(), stays = emptyList(), rules = emptyList())
        assertEquals(listOf("no positions 00:00–24:00"), Places.list(day).map { it.text })
    }

    @Test
    fun thePhonesOwnPositionsAreAddedOnce() {
        val server = testDay()
        val local = listOf(
            RuleFix(at("08:17").plusMillis(900), pier.first, pier.second, 8.0), // the same as the server's
            fix("12:40", cafe),
        )
        val merged = Places.merge(server, "2026-11-20", zone, server.from, server.to, local, emptyList())
        assertEquals(server.positions.size + 1, merged.positions.size)
        assertTrue(merged.positions.any { it.at == at("12:40") })
        // Without an answer: the phone's positions and the saved habit places.
        val alone = Places.merge(null, "2026-11-20", zone, at("00:00"), at("13:00"), local, server.rules)
        assertEquals(2, alone.positions.size)
        assertEquals(server.rules, alone.rules)
        assertEquals("no positions 00:00–08:17", Places.list(alone).first().text)
    }

    @Test
    fun theServersAnswerIsRead() {
        val json = """{"date":"2026-11-20","timezone":"Europe/Istanbul","from":"2026-11-19T21:00:00Z","to":"2026-11-20T21:00:00Z",
            "positions":[{"at":"2026-11-20T05:17:00Z","lat":41.0,"lon":29.0,"accuracy":12}],
            "stays":[{"from":"2026-11-20T06:15:00Z","to":"2026-11-20T06:50:00Z","lat":41.03,"lon":29.02,"home":false,"nearby":null}],
            "rules":[{"habit_id":1,"habit":"Walk","name":"Test pier","lat":41.0,"lon":29.0,"radius_m":100}]}"""
        val day = assertNotNullAnd(Places.parse(json))
        assertEquals(1, day.positions.size)
        assertEquals(emptyList<NearbyPlace>(), day.stays.single().nearby)
        assertEquals("Test pier", day.rules.single().name)
        assertEquals("08:17 Test pier · under 1 min", Places.list(day).first { it.kind == PlaceItem.Kind.VISIT }.text)
    }

    @Test
    fun theLocalLogReadsItsOwnLines() {
        assertEquals(RuleFix(Instant.ofEpochMilli(1_700_000_000_000), 41.0, 29.0, 12.5), FixLog.parseLine("1700000000000,41.0,29.0,12.5"))
        assertEquals(null, FixLog.parseLine("1700000000000,91.0,29.0,12.5"))
        assertEquals(null, FixLog.parseLine("garbage"))
        val dir = java.nio.file.Files.createTempDirectory("fixes").toFile()
        val log = FixLog(dir)
        log.record(fix("23:59", home), zone)
        log.record(RuleFix(at("23:59").plusSeconds(120), cafe.first, cafe.second, 5.0), zone)
        assertEquals(2, log.between(at("23:00"), at("23:59").plusSeconds(3600), zone).size)
        assertEquals(1, log.between(at("23:00"), at("23:59").plusSeconds(60), zone).size)
    }

    private fun <T> assertNotNullAnd(value: T?): T {
        assertNotNull(value)
        return value!!
    }
}
