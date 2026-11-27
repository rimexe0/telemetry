package cyou.rime.telemetry.collector

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.LocalDate

class PeriodsTest {
    private val today = LocalDate.parse("2026-11-27") // a Friday

    private fun date(text: String) = LocalDate.parse(text)

    @Test
    fun weeksStartOnMonday() {
        assertEquals(Period(Scope.WEEK, date("2026-11-23"), date("2026-11-29")), Periods.of(Scope.WEEK, today))
        // A Sunday is the end of its week, a Monday the start of the next.
        assertEquals(date("2026-11-23"), Periods.of(Scope.WEEK, date("2026-11-29")).start)
        assertEquals(date("2026-11-30"), Periods.of(Scope.WEEK, date("2026-11-30")).start)
        assertEquals(Period(Scope.MONTH, date("2026-02-01"), date("2026-02-28")), Periods.of(Scope.MONTH, date("2026-02-14")))
        assertEquals(Period(Scope.YEAR, date("2026-01-01"), date("2026-12-31")), Periods.of(Scope.YEAR, today))
    }

    @Test
    fun steppingMovesByThePeriodAndNeverIntoTheFuture() {
        assertEquals(date("2026-11-20"), Periods.step(Scope.WEEK, today, -1, today))
        assertEquals(date("2026-10-27"), Periods.step(Scope.MONTH, today, -1, today))
        assertEquals(date("2025-11-27"), Periods.step(Scope.YEAR, today, -1, today))
        assertEquals(date("2026-11-26"), Periods.step(Scope.DAY, today, -1, today))
        // Nothing after the period holding today.
        assertNull(Periods.step(Scope.WEEK, today, 1, today))
        assertNull(Periods.step(Scope.MONTH, today, 1, today))
        assertNull(Periods.step(Scope.YEAR, today, 1, today))
        assertNull(Periods.step(Scope.DAY, today, 1, today))
        // Forward into the current period lands on today at the latest.
        assertEquals(today, Periods.step(Scope.MONTH, date("2026-10-30"), 1, today))
        assertEquals(date("2026-11-24"), Periods.step(Scope.WEEK, date("2026-11-17"), 1, today))
    }

    @Test
    fun aPeriodsDaysStopAtToday() {
        val week = Periods.of(Scope.WEEK, today)
        assertEquals((23..27).map { date("2026-11-$it") }, Periods.days(week, today))
        assertTrue(Periods.isCurrent(week, today))
        assertFalse(Periods.isCurrent(Periods.of(Scope.WEEK, date("2026-11-20")), today))
        assertEquals(30, Periods.days(Periods.of(Scope.MONTH, date("2026-09-03")), today).size)
    }

    @Test
    fun labels() {
        assertEquals("Today", Periods.label(Periods.of(Scope.DAY, today), today))
        assertEquals("Thu 26 Nov", Periods.label(Periods.of(Scope.DAY, date("2026-11-26")), today))
        assertEquals("23–29 Nov", Periods.label(Periods.of(Scope.WEEK, today), today))
        assertEquals("30 Nov – 6 Dec", Periods.label(Periods.of(Scope.WEEK, date("2026-12-01")), date("2026-12-02")))
        assertEquals("24–30 Nov 2025", Periods.label(Periods.of(Scope.WEEK, date("2025-11-26")), today))
        assertEquals("29 Dec 2025 – 4 Jan 2026", Periods.label(Periods.of(Scope.WEEK, date("2026-01-01")), today))
        assertEquals("Nov 2026", Periods.label(Periods.of(Scope.MONTH, today), today))
        assertEquals("2026", Periods.label(Periods.of(Scope.YEAR, today), today))
        assertEquals("The week before", Periods.stepName(Scope.WEEK, forward = false))
    }
}

class PeriodModelTest {
    private fun row(date: String, distraction: Double, mac: Double? = null, phone: Double? = null, agent: Double? = 0.0, final: Boolean = true, version: String = "v1",
                    positions: Int = 0, outside: Double? = null, km: Double? = null, stays: List<RowStay> = emptyList(), apps: List<RowUsage> = emptyList()) =
        DayRow(date, final, version, 1440.0, distraction,
            listOf(RowDevice("mac", mac ?: 0.0, 0.0, if (mac == null) 1440.0 else 0.0, mac != null), RowDevice("phone", phone ?: 0.0, distraction, if (phone == null) 1440.0 else 0.0, phone != null)),
            agent, apps, emptyList(), "", positions, outside, km, stays)

    private val days = (23..27).map { LocalDate.parse("2026-11-$it") }

    @Test
    fun averagesLeaveOutDaysWithoutData() {
        val rows = listOf(
            row("2026-11-23", 60.0, phone = 120.0, apps = listOf(RowUsage("phone", "YouTube", "distraction", 60.0))),
            // Nothing reported this day: not a day of zero.
            row("2026-11-24", 0.0),
            row("2026-11-25", 30.0, mac = 300.0, phone = 60.0, agent = 90.0, apps = listOf(RowUsage("phone", "YouTube", "distraction", 30.0))),
        ).associateBy { it.date }
        val view = PeriodModel.activity(days, rows)
        assertEquals(Average(45.0, 2), view.distraction)
        assertEquals(Average(45.0, 2), view.agents)
        val phone = view.devices.first { it.device == "phone" }
        assertEquals(Average(90.0, 2), phone.active)
        assertEquals(listOf(PeriodUsage("YouTube", "distraction", 45.0)), phone.apps)
        assertEquals(Average(300.0, 1), view.devices.first { it.device == "mac" }.active)
        // Days never kept show as nothing, in their place.
        assertEquals(listOf(true, true, true, false, false), view.rows.map { it != null })
        assertNull(Average.of(listOf(null, null)))
        assertEquals("average of 2 days with data", PeriodText.over(2))
        assertEquals("1 day with data", PeriodText.over(1))
        assertEquals("45 min a day", PeriodText.perDay(view.distraction))
    }

    @Test
    fun placesAverageOverDaysWithPositionsAndGroupStays() {
        val at = { day: Int, hour: Int -> Instant.parse("2026-11-${day}T${"%02d".format(hour)}:00:00Z") }
        val cafe = RowStay(at(23, 9), at(23, 10), 41.0, 29.0, false, "Kahve")
        val rows = listOf(
            row("2026-11-23", 0.0, positions = 40, outside = 120.0, km = 4.0, stays = listOf(cafe, RowStay(at(23, 0), at(23, 8), 41.02, 29.0, true, ""))),
            row("2026-11-24", 0.0, positions = 30, outside = 60.0, km = 2.0, stays = listOf(cafe.copy(from = at(24, 9), to = at(24, 11)))),
            row("2026-11-25", 0.0),
        ).associateBy { it.date }
        val view = PeriodModel.places(days, rows, emptyList())
        assertEquals(Average(90.0, 2), view.away)
        assertEquals(Average(3.0, 2), view.distanceKm)
        assertEquals("Kahve · 2 days · 3 h", PeriodModel.placeLine(view.places.first()))
        assertEquals("home", view.places[1].name)
        // A habit's place names the stays inside it.
        val rule = HabitPlace(1, "Coffee", "Corner", 41.0, 29.0, 100)
        assertEquals("Corner", PeriodModel.places(days, rows, listOf(rule)).places.first().name)
    }

    @Test
    fun onlyDaysThatMayChangeAreAskedAgain() {
        val rows = listOf(row("2026-11-23", 0.0), row("2026-11-24", 0.0, final = false), row("2026-11-25", 0.0, version = "v0")).associateBy { it.date }
        assertEquals(days.drop(1), PeriodModel.stale(days, rows, "v1"))
        assertEquals(listOf(days[1], days[3], days[4]), PeriodModel.stale(days, rows, null))
        val ranges = PeriodModel.ranges(listOf(days[0], days[1], days[3]))
        assertEquals(listOf(days[0] to days[1], days[3] to days[3]), ranges)
        val year = (0 until 100L).map { LocalDate.parse("2026-01-01").plusDays(it) }
        assertEquals(listOf(62, 38), PeriodModel.ranges(year).map { (from, to) -> java.time.temporal.ChronoUnit.DAYS.between(from, to).toInt() + 1 })
    }

    @Test
    fun aRowReadsAsTheServerSendsIt() {
        val json = """{"version":"abc","days":[{"date":"2026-11-25","final":true,"minutes":1440,"distraction_minutes":42.5,
            "devices":[{"device":"phone","active_minutes":100,"distraction_minutes":42.5,"no_data_minutes":10,"reported":true}],
            "agent_minutes":null,"apps":[{"device":"phone","name":"com.whatsapp","app_name":"WhatsApp","minutes":12}],"sites":[],
            "cells":"da.n_","positions":0,"outside_minutes":null,"distance_km":null,"stays":[]}]}"""
        val (version, rows) = PeriodModel.parse(json)!!
        assertEquals("abc", version)
        val row = rows.single()
        assertEquals("abc", row.version)
        assertNull(row.agentMinutes)
        assertNull(row.outsideMinutes)
        assertEquals("WhatsApp", row.apps.single().name)
        assertEquals(listOf(Cell.DISTRACTION, Cell.ACTIVE, Cell.IDLE, Cell.NO_DATA, Cell.LATER), PeriodModel.cells(row))
        // As kept on the phone it keeps its labels' version.
        val kept = PeriodModel.keep(org.json.JSONObject(json).getJSONArray("days").getJSONObject(0), "abc")
        assertEquals(row, PeriodModel.row(kept))
    }
}
