package budget

import (
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// noBedtimeStart is when a budget day starts with bedtime off.
const noBedtimeStart = "04:00"

func location(config policy.Config) *time.Location {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

// dayDate is a day key's date at midnight UTC, for counting days.
func dayDate(day string) time.Time {
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}
	}
	return date
}

// AddDays moves a day key by n days.
func AddDays(day string, n int) string {
	return dayDate(day).AddDate(0, 0, n).Format("2006-01-02")
}

// DayStart is when a budget day starts: the end of the bedtime in force that
// day, the wake time the owner chose, or 04:00 with bedtime off. The day's
// key is the local date it starts on.
func DayStart(config policy.Config, day string) time.Time {
	zone := location(config)
	noon, err := time.ParseInLocation("2006-01-02 15:04", day+" 12:00", zone)
	if err != nil {
		return time.Time{}
	}
	clock := noBedtimeStart
	if bedtime := config.BedtimeAt(noon); bedtime.Enabled {
		clock = bedtime.End
	}
	start, err := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, zone)
	if err != nil {
		return time.Time{}
	}
	return start.UTC()
}

// DayEnd is when a budget day ends: the next one's start.
func DayEnd(config policy.Config, day string) time.Time {
	return DayStart(config, AddDays(day, 1))
}

// DayOf is the budget day an instant belongs to; use at 01:30 counts against
// the day before.
func DayOf(config policy.Config, at time.Time) string {
	day := at.In(location(config)).Format("2006-01-02")
	if at.Before(DayStart(config, day)) {
		return AddDays(day, -1)
	}
	return day
}

// BedtimeStart is when bedtime starts within a budget day, if it does. Habits
// can be ticked until then, and the day's habits close then.
func BedtimeStart(config policy.Config, day string) (time.Time, bool) {
	zone := location(config)
	start, end := DayStart(config, day), DayEnd(config, day)
	// A changed bedtime starts at a wake time, so the rule in force when the
	// day starts is the day's.
	bedtime := config.BedtimeAt(start)
	if !bedtime.Enabled {
		return time.Time{}, false
	}
	for offset := 0; offset <= 1; offset++ {
		date := AddDays(day, offset)
		at, err := time.ParseInLocation("2006-01-02 15:04", date+" "+bedtime.Start, zone)
		if err == nil && at.After(start) && !at.After(end) {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// CloseAt is when a budget day's habits are settled: bedtime's start, or the
// day's end without one.
func CloseAt(config policy.Config, day string) time.Time {
	if at, ok := BedtimeStart(config, day); ok {
		return at
	}
	return DayEnd(config, day)
}
