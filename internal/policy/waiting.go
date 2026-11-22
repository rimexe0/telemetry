package policy

import (
	"reflect"
	"time"
)

// ruleAt is the rule in force at an instant: a looser rule waiting for its
// day replaces the current one from that moment.
func ruleAt[T any](timezone string, current T, next *T, from string, at time.Time) T {
	if next == nil || from == "" {
		return current
	}
	if Started(timezone, from, at) {
		return *next
	}
	return current
}

// Started reports whether a waiting rule's start has come at an instant.
// The start is a moment (RFC 3339): the wake time after the next bedtime.
// Rules set before 2026-11-22 waited for a local date (YYYY-MM-DD) instead,
// and start at that date's midnight.
func Started(timezone, from string, at time.Time) bool {
	if from == "" {
		return false
	}
	if moment, err := time.Parse(time.RFC3339, from); err == nil {
		return !at.Before(moment)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	return at.In(location).Format("2006-01-02") >= from
}

// NextDay is when a looser rule set now starts. With a bedtime in force it
// is the end of the next bedtime that starts after now: the wake time the
// owner picked, after a whole night under the rules that held when they
// asked. Loosening late at night therefore never lands at midnight, the
// start of the night it was meant to spare (owner request 2026-11-22, after
// bedtime switched off at 23:59 took effect a minute later). Without a
// bedtime, the next local midnight.
func (c Config) NextDay(now time.Time) string {
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		location = time.UTC
	}
	bedtime := c.BedtimeAt(now)
	if bedtime.Enabled && bedtime.Start != bedtime.End {
		local := now.In(location)
		for offset := 0; offset <= 2; offset++ {
			day := local.AddDate(0, 0, offset).Format("2006-01-02")
			start, err1 := time.ParseInLocation("2006-01-02 15:04", day+" "+bedtime.Start, location)
			end, err2 := time.ParseInLocation("2006-01-02 15:04", day+" "+bedtime.End, location)
			if err1 != nil || err2 != nil || !start.After(now) {
				continue
			}
			if !end.After(start) {
				end = end.AddDate(0, 0, 1)
			}
			return end.UTC().Format(time.RFC3339)
		}
	}
	return now.In(location).AddDate(0, 0, 1).Format("2006-01-02")
}

// withRule sets a rule as of now. A rule that asks less than the one in
// force waits until nextDay, and until then the rule in force stays; one
// that asks the same or more applies at once. Setting the rule in force
// again also settles a waiting rule whose day has come.
func withRule[T any](timezone string, current T, next *T, from string, incoming T, looser func(current, next T) bool, now time.Time, nextDay string) (T, *T, string) {
	inForce := ruleAt(timezone, current, next, from, now)
	if next != nil && reflect.DeepEqual(inForce, *next) && reflect.DeepEqual(incoming, inForce) {
		return inForce, nil, ""
	}
	if looser(inForce, incoming) {
		return inForce, &incoming, nextDay
	}
	if reflect.DeepEqual(incoming, inForce) && next != nil {
		// Nothing asked of the rule: a looser one keeps waiting.
		return current, next, from
	}
	return incoming, nil, ""
}
