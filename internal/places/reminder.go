package places

import (
	"math"
	"time"
)

// Where a position is relative to a reminder's place. The phone app keeps
// the same rule (PlaceRule.kt); keep the two in step.
type Where int

const (
	Unsure Where = iota
	Inside
	Outside
)

// Circle is one place a reminder watches: inside within Radius meters.
type Circle struct {
	Lat, Lon, Radius float64
}

// Seen is what the positions showed since a reminder was set.
type Seen struct {
	WasIn, WasOut bool
}

const (
	// placeEdge keeps GPS wobble at a circle's edge from counting as coming
	// and going: outside means this far past it.
	placeEdge = 100.0
	// placeWorstAccuracy is the poorest fix that decides anything.
	placeWorstAccuracy = 100.0
)

// Locate says whether a fix is inside any of the circles.
func Locate(circles []Circle, fix Fix) Where {
	if fix.Accuracy > placeWorstAccuracy || len(circles) == 0 {
		return Unsure
	}
	past := math.Inf(1)
	for _, circle := range circles {
		past = math.Min(past, Meters(fix.Lat, fix.Lon, circle.Lat, circle.Lon)-circle.Radius)
	}
	switch {
	case past <= 0:
		return Inside
	case past > placeEdge:
		return Outside
	}
	return Unsure
}

// FirstLook is the state when a reminder is set. An unsure first look counts
// as away for an arrival.
func FirstLook(arrive bool, where Where) Seen {
	return Seen{WasIn: where == Inside, WasOut: where == Outside || (arrive && where == Unsure)}
}

// Look adds one more position.
func (s Seen) Look(where Where) Seen {
	return Seen{WasIn: s.WasIn || where == Inside, WasOut: s.WasOut || where == Outside}
}

// Fires says whether a reminder comes due at this look, given what was seen
// before it: an arrival after being away, a departure after being inside,
// and never before notBefore.
func Fires(arrive bool, before Seen, where Where, at, notBefore time.Time) bool {
	if at.Before(notBefore) {
		return false
	}
	if arrive {
		return where == Inside && before.WasOut
	}
	return where == Outside && before.WasIn
}

// Reached runs the rule over the positions since a reminder was set: last
// is the latest position before it was set, if any, and fixes those after,
// oldest first. It returns when the place was reached, or nil.
func Reached(arrive bool, circles []Circle, last *Fix, fixes []Fix, notBefore, now time.Time) *time.Time {
	first := Unsure
	if last != nil {
		first = Locate(circles, *last)
	}
	seen := FirstLook(arrive, first)
	where := first
	for _, fix := range fixes {
		where = Locate(circles, fix)
		if Fires(arrive, seen, where, fix.At, notBefore) {
			at := fix.At
			return &at
		}
		seen = seen.Look(where)
	}
	// Reached before the earliest time and still there.
	if len(fixes) > 0 && Fires(arrive, seen, where, now, notBefore) {
		return &now
	}
	return nil
}
