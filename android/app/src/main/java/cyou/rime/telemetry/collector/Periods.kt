package cyou.rime.telemetry.collector

import java.time.DayOfWeek
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.time.temporal.TemporalAdjusters
import java.util.Locale

/** How much of time Activity shows at once; the same for all four of its views. */
enum class Scope(val label: String, val unit: String) {
    DAY("Day", "day"), WEEK("Week", "week"), MONTH("Month", "month"), YEAR("Year", "year")
}

/** A day, a week from Monday, a calendar month or a calendar year; [end] is its last day. */
data class Period(val scope: Scope, val start: LocalDate, val end: LocalDate) {
    /** Names the period, for saved answers and the map's fit. */
    val key: String get() = "${scope.unit}-$start"
}

object Periods {
    /** The period of [scope] that holds [day]. Weeks start on Monday. */
    fun of(scope: Scope, day: LocalDate): Period = when (scope) {
        Scope.DAY -> Period(scope, day, day)
        Scope.WEEK -> day.with(TemporalAdjusters.previousOrSame(DayOfWeek.MONDAY)).let { Period(scope, it, it.plusDays(6)) }
        Scope.MONTH -> day.withDayOfMonth(1).let { Period(scope, it, it.plusMonths(1).minusDays(1)) }
        Scope.YEAR -> day.withDayOfYear(1).let { Period(scope, it, it.plusYears(1).minusDays(1)) }
    }

    /**
     * The day shown after stepping [by] periods from [day]: the same day of
     * the week, month or year, never past [today]; null when that period
     * would start after today.
     */
    fun step(scope: Scope, day: LocalDate, by: Long, today: LocalDate): LocalDate? {
        val next = when (scope) {
            Scope.DAY -> day.plusDays(by)
            Scope.WEEK -> day.plusWeeks(by)
            Scope.MONTH -> day.plusMonths(by)
            Scope.YEAR -> day.plusYears(by)
        }
        if (of(scope, next).start.isAfter(today)) return null
        return if (next.isAfter(today)) today else next
    }

    /** True while [today] is in [period]: Today is then not offered. */
    fun isCurrent(period: Period, today: LocalDate): Boolean = !today.isBefore(period.start) && !today.isAfter(period.end)

    /** The period's days up to [today], oldest first: none in the future. */
    fun days(period: Period, today: LocalDate): List<LocalDate> {
        val last = if (period.end.isAfter(today)) today else period.end
        val days = mutableListOf<LocalDate>()
        var day = period.start
        while (!day.isAfter(last)) {
            days += day
            day = day.plusDays(1)
        }
        return days
    }

    private val dayMonth = DateTimeFormatter.ofPattern("d MMM", Locale.UK)
    private val monthYear = DateTimeFormatter.ofPattern("MMM yyyy", Locale.UK)
    private val weekday = DateTimeFormatter.ofPattern("EEE d MMM", Locale.UK)

    /**
     * The switcher's words: "Today" or "Fri 27 Nov"; "24–30 Nov",
     * "30 Nov – 6 Dec", with the year when it is not this one; "Nov 2026";
     * "2026".
     */
    fun label(period: Period, today: LocalDate): String = when (period.scope) {
        Scope.DAY -> if (period.start == today) "Today" else weekday.format(period.start)
        Scope.WEEK -> {
            val (start, end) = period.start to period.end
            val year = if (start.year != today.year || end.year != today.year) " ${end.year}" else ""
            when {
                start.year != end.year -> "${dayMonth.format(start)} ${start.year} – ${dayMonth.format(end)} ${end.year}"
                start.month == end.month -> "${start.dayOfMonth}–${dayMonth.format(end)}$year"
                else -> "${dayMonth.format(start)} – ${dayMonth.format(end)}$year"
            }
        }
        Scope.MONTH -> monthYear.format(period.start)
        Scope.YEAR -> period.start.year.toString()
    }

    /** "The week before", "The month after": the switcher's buttons, read aloud. */
    fun stepName(scope: Scope, forward: Boolean): String = "The ${scope.unit} ${if (forward) "after" else "before"}"
}

/** An average per day over the days that have a value; days without one are left out, never counted as zero. */
data class Average(val perDay: Double, val days: Int) {
    companion object {
        fun of(values: List<Double?>): Average? {
            val known = values.filterNotNull()
            return if (known.isEmpty()) null else Average(known.sum() / known.size, known.size)
        }
    }
}

object PeriodText {
    /** "average of 5 days with data", "1 day with data". */
    fun over(days: Int): String = if (days == 1) "1 day with data" else "average of $days days with data"

    /** "1 h 12 min a day", or "no data" without any day to average. */
    fun perDay(average: Average?): String = if (average == null) "no data" else "${ActivityText.minutes(average.perDay)} a day"
}
