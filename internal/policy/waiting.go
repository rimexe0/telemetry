package policy

import (
	"reflect"
	"time"
)

// ruleAt is the rule in force at an instant: a looser rule waiting for its
// day replaces the current one from that local date.
func ruleAt[T any](timezone string, current T, next *T, from string, at time.Time) T {
	if next == nil || from == "" {
		return current
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	if at.In(location).Format("2006-01-02") >= from {
		return *next
	}
	return current
}

// withRule sets a rule as of now. A rule that asks less than the one in
// force waits for the next local day, and today keeps the rule in force; one
// that asks the same or more applies at once. Setting the rule in force again
// also settles a waiting rule whose day has come.
func withRule[T any](timezone string, current T, next *T, from string, incoming T, looser func(current, next T) bool, now time.Time) (T, *T, string) {
	inForce := ruleAt(timezone, current, next, from, now)
	if next != nil && reflect.DeepEqual(inForce, *next) && reflect.DeepEqual(incoming, inForce) {
		return inForce, nil, ""
	}
	if looser(inForce, incoming) {
		location, err := time.LoadLocation(timezone)
		if err != nil {
			location = time.UTC
		}
		return inForce, &incoming, now.In(location).AddDate(0, 0, 1).Format("2006-01-02")
	}
	if reflect.DeepEqual(incoming, inForce) && next != nil {
		// Nothing asked of the rule: a looser one keeps waiting.
		return current, next, from
	}
	return incoming, nil, ""
}
