package budget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/places"
	"github.com/rimexe0/telemetry/internal/policy"
)

// Habit rules (owner request 2026-11-26): a habit may have a rule that
// ticks it by itself once the records prove it. The one kind so far is
// "place": be within radius_m of a place the owner picked, for minutes,
// within a window of the morning. The phone works the rule out first, so it
// works while the server is down; the server works it out again from the
// stored positions and the watch's walks as a safety net. Missing positions
// are missing: nothing is ever ticked on a guess, and once the owner ticked
// or unticked a habit on a day, the rule leaves that day alone.

const (
	// RuleKindPlace is a place to reach.
	RuleKindPlace = "place"
	// RuleFromWake opens the window when the owner wakes.
	RuleFromWake = "wake"
	// RuleSourcePrefix marks a tick a rule made: "rule:server" or
	// "rule:device:<id>".
	RuleSourcePrefix = "rule"

	defaultRuleRadius  = 100
	minRuleRadius      = 20
	maxRuleRadius      = 2000
	defaultRuleMinutes = 1
	maxRuleMinutes     = 240

	// ruleAccuracy is the poorest position that counts, as for the
	// outside rule.
	ruleAccuracy = 100.0
	// ruleGap is the longest gap between two positions inside the radius
	// that still counts as staying inside throughout.
	ruleGap = 15 * time.Minute
	// wakeBefore is how long before the day's start a recorded wake still
	// belongs to the day (up before the end of bedtime).
	wakeBefore = 12 * time.Hour
)

// RulePlace is where a place rule asks the owner to go.
type RulePlace struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// RuleWindow is when the rule counts: from "wake" or a local "HH:MM", until
// a local "HH:MM", on the budget day.
type RuleWindow struct {
	From  string `json:"from"`
	Until string `json:"until"`
}

// HabitRule ticks a habit by itself.
type HabitRule struct {
	Kind    string     `json:"kind"`
	Place   *RulePlace `json:"place,omitempty"`
	RadiusM int        `json:"radius_m,omitempty"`
	Window  RuleWindow `json:"window"`
	// Minutes is the time inside the radius needed.
	Minutes int `json:"minutes,omitempty"`
}

// ErrInvalidRule is a rule that cannot be kept.
var ErrInvalidRule = errors.New("invalid habit rule")

// Normalize fills the defaults (100 m, 1 minute) and refuses a rule that
// cannot be worked out.
func (r *HabitRule) Normalize() error {
	r.Kind = strings.TrimSpace(r.Kind)
	if r.Kind != RuleKindPlace {
		return fmt.Errorf("%w: kind must be %q", ErrInvalidRule, RuleKindPlace)
	}
	if r.Place == nil {
		return fmt.Errorf("%w: a place rule needs a place", ErrInvalidRule)
	}
	r.Place.Name = strings.TrimSpace(r.Place.Name)
	if r.Place.Name == "" || len([]rune(r.Place.Name)) > 100 {
		return fmt.Errorf("%w: a place name is 1 to 100 characters", ErrInvalidRule)
	}
	if math.IsNaN(r.Place.Lat) || math.IsNaN(r.Place.Lon) || r.Place.Lat < -90 || r.Place.Lat > 90 || r.Place.Lon < -180 || r.Place.Lon > 180 ||
		(r.Place.Lat == 0 && r.Place.Lon == 0) {
		return fmt.Errorf("%w: the place needs a latitude and longitude", ErrInvalidRule)
	}
	if r.RadiusM == 0 {
		r.RadiusM = defaultRuleRadius
	}
	if r.RadiusM < minRuleRadius || r.RadiusM > maxRuleRadius {
		return fmt.Errorf("%w: radius_m is %d to %d", ErrInvalidRule, minRuleRadius, maxRuleRadius)
	}
	if r.Minutes == 0 {
		r.Minutes = defaultRuleMinutes
	}
	if r.Minutes < 1 || r.Minutes > maxRuleMinutes {
		return fmt.Errorf("%w: minutes is 1 to %d", ErrInvalidRule, maxRuleMinutes)
	}
	r.Window.From, r.Window.Until = strings.TrimSpace(r.Window.From), strings.TrimSpace(r.Window.Until)
	until, ok := clockMinutes(r.Window.Until)
	if !ok {
		return fmt.Errorf("%w: window.until is a local time HH:MM", ErrInvalidRule)
	}
	if r.Window.From != RuleFromWake {
		from, ok := clockMinutes(r.Window.From)
		if !ok {
			return fmt.Errorf("%w: window.from is \"wake\" or a local time HH:MM", ErrInvalidRule)
		}
		if from == until {
			return fmt.Errorf("%w: the window is empty", ErrInvalidRule)
		}
	}
	return nil
}

func clockMinutes(clock string) (int, bool) {
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

// RuleField is a rule given in a change: absent leaves it, null removes it,
// an object sets it.
type RuleField struct {
	Set  bool
	Rule *HabitRule
}

func (f *RuleField) UnmarshalJSON(data []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Rule = nil
		return nil
	}
	var rule HabitRule
	if err := json.Unmarshal(data, &rule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRule, err)
	}
	f.Rule = &rule
	return nil
}

// RuleSpan is the window of a rule on one budget day, resolved to times.
type RuleSpan struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
	// WakeKnown is false for a window from waking when the records do not
	// show the wake yet: it then opens at the day's start (the end of
	// bedtime, the wake time the owner chose; 04:00 with bedtime off).
	WakeKnown bool `json:"wake_known"`
}

// clockOn is a local HH:MM within the budget day: on the day's date, or the
// next date when that is before the day's start.
func clockOn(config policy.Config, day, clock string, start time.Time) (time.Time, bool) {
	at, err := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, location(config))
	if err != nil {
		return time.Time{}, false
	}
	if at.Before(start) {
		at = at.AddDate(0, 0, 1)
	}
	return at.UTC(), true
}

// SpanOn resolves a rule's window on a budget day. wake is the owner's
// recorded wake, or nil when the records do not show it. The window never
// starts before the day does and never runs past bedtime, when the day's
// habits close. ok is false when nothing is left of it.
func SpanOn(config policy.Config, rule HabitRule, day string, wake *time.Time) (RuleSpan, bool) {
	start, end := DayStart(config, day), CloseAt(config, day)
	until, ok := clockOn(config, day, rule.Window.Until, start)
	if !ok {
		return RuleSpan{}, false
	}
	if until.After(end) {
		until = end
	}
	span := RuleSpan{From: start, Until: until}
	if rule.Window.From == RuleFromWake {
		if wake != nil && !wake.Before(start.Add(-wakeBefore)) && wake.Before(DayEnd(config, day)) {
			span.WakeKnown = true
			if wake.After(start) {
				span.From = wake.UTC()
			}
		}
	} else {
		from, ok := clockOn(config, day, rule.Window.From, start)
		if !ok {
			return RuleSpan{}, false
		}
		span.From = from
	}
	return span, span.From.Before(span.Until)
}

// insideFix is a position good enough to count within radius meters of the
// place.
func insideFix(rule HabitRule, fix places.Fix) bool {
	return fix.Accuracy <= ruleAccuracy && places.Meters(fix.Lat, fix.Lon, rule.Place.Lat, rule.Place.Lon) <= float64(rule.RadiusM)
}

// PlaceReached is the moment the positions first add up to rule.Minutes
// inside the radius within [from, until): the stretches between consecutive
// positions of one device that are both inside, at most ruleGap apart, each
// moment counted once however many devices saw it. Nil when they do not:
// missing positions are never filled in.
func PlaceReached(rule HabitRule, tracks [][]places.Fix, from, until time.Time) *time.Time {
	if rule.Kind != RuleKindPlace || rule.Place == nil || !from.Before(until) {
		return nil
	}
	intervals := []places.Interval{}
	for _, track := range tracks {
		ordered := append([]places.Fix(nil), track...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
		for i := 1; i < len(ordered); i++ {
			a, b := ordered[i-1], ordered[i]
			gap := b.At.Sub(a.At)
			if gap <= 0 || gap > ruleGap || !insideFix(rule, a) || !insideFix(rule, b) {
				continue
			}
			interval := places.Interval{From: a.At, To: b.At}
			if interval.From.Before(from) {
				interval.From = from
			}
			if interval.To.After(until) {
				interval.To = until
			}
			if interval.To.After(interval.From) {
				intervals = append(intervals, interval)
			}
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].From.Before(intervals[j].From) })
	need := time.Duration(rule.Minutes) * time.Minute
	total := time.Duration(0)
	var current *places.Interval
	for i := range intervals {
		interval := intervals[i]
		if current != nil && !interval.From.After(current.To) {
			if interval.To.After(current.To) {
				current.To = interval.To
			}
			continue
		}
		if current != nil {
			if reached := reachedIn(*current, total, need); reached != nil {
				return reached
			}
			total += current.To.Sub(current.From)
		}
		current = &interval
	}
	if current != nil {
		return reachedIn(*current, total, need)
	}
	return nil
}

// reachedIn is when an interval brings the total so far to need, rounded up
// to the second (never past the interval's end).
func reachedIn(interval places.Interval, total, need time.Duration) *time.Time {
	if total+interval.To.Sub(interval.From) < need {
		return nil
	}
	at := interval.From.Add(need - total)
	if rounded := at.Truncate(time.Second); rounded.Before(at) {
		at = rounded.Add(time.Second)
	}
	if at.After(interval.To) {
		at = interval.To
	}
	at = at.UTC()
	return &at
}

// ByRule reports whether a tick was made by a habit rule.
func ByRule(source string) bool {
	return source == RuleSourcePrefix || strings.HasPrefix(source, RuleSourcePrefix+":")
}
