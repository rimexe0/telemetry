package policy

import (
	"fmt"
	"reflect"
	"slices"
	"time"
)

// ModeOutside holds distractions, or everything but the essentials, until
// the owner has been outside long enough today (owner request 2026-10-15).
const ModeOutside = "outside"

const (
	// HoldDistractions keeps distractions closed, distraction times included.
	HoldDistractions = "distractions"
	// HoldLock closes everything but the essentials.
	HoldLock = "lock"
)

// OutsideRule asks for time outside each day before what it holds opens.
// Every number is the owner's to set; nothing here is a recommendation.
type OutsideRule struct {
	Enabled bool `json:"enabled"`
	// Minutes outside needed each local day.
	Minutes int `json:"minutes"`
	// Meters from home a position must be to count as outside.
	Meters int `json:"meters"`
	// Holds is what stays shut until the minutes are met: HoldDistractions
	// and/or HoldLock.
	Holds []string `json:"holds"`
	// FromWake starts the hold when the owner wakes; From starts it at a
	// local "HH:MM". With neither it holds from midnight.
	FromWake bool   `json:"from_wake,omitempty"`
	From     string `json:"from,omitempty"`
	// Until is a local "HH:MM" after which the hold gives up for the day.
	Until string `json:"until,omitempty"`
	// WatchOnly counts only time the watch proves, worn, outside; otherwise
	// the phone's positions count too.
	WatchOnly bool `json:"watch_only,omitempty"`
	// OnFoot leaves out stretches faster than walking, such as a car or bus.
	OnFoot bool `json:"on_foot,omitempty"`
	// AllowOpenings lets an opening the assistant makes lift the hold.
	AllowOpenings bool `json:"allow_openings,omitempty"`
}

// Normalize validates a rule. A disabled rule keeps its numbers so it can be
// switched back on as it was.
func (r *OutsideRule) Normalize() error {
	if !r.Enabled {
		return nil
	}
	switch {
	case r.Minutes < 1 || r.Minutes > 600:
		return fmt.Errorf("%w: outside.minutes must be between 1 and 600", ErrInvalidConfig)
	case r.Meters < 30 || r.Meters > 50000:
		return fmt.Errorf("%w: outside.meters must be between 30 and 50000", ErrInvalidConfig)
	case len(r.Holds) == 0:
		return fmt.Errorf("%w: outside.holds needs %q, %q or both", ErrInvalidConfig, HoldDistractions, HoldLock)
	case r.From != "" && !clockPattern.MatchString(r.From), r.Until != "" && !clockPattern.MatchString(r.Until):
		return fmt.Errorf("%w: outside.from and outside.until must be HH:MM", ErrInvalidConfig)
	}
	for _, hold := range r.Holds {
		if hold != HoldDistractions && hold != HoldLock {
			return fmt.Errorf("%w: outside.holds may only contain %q and %q", ErrInvalidConfig, HoldDistractions, HoldLock)
		}
	}
	slices.Sort(r.Holds)
	r.Holds = slices.Compact(r.Holds)
	return nil
}

// Looser reports whether next asks less of the owner than r in any way. A
// looser rule only takes effect the next day, so it cannot be used to get
// out of today's walk.
func (r OutsideRule) Looser(next OutsideRule) bool {
	if !r.Enabled {
		return false
	}
	if !next.Enabled {
		return true
	}
	for _, hold := range r.Holds {
		if !slices.Contains(next.Holds, hold) {
			return true
		}
	}
	later := func(a, b string) bool { return a == "" && b != "" || a != "" && b != "" && b > a }
	earlier := func(a, b string) bool { return a == "" && b != "" || a != "" && b != "" && b < a }
	return next.Minutes < r.Minutes || next.Meters < r.Meters ||
		(r.WatchOnly && !next.WatchOnly) || (r.OnFoot && !next.OnFoot) || (!r.AllowOpenings && next.AllowOpenings) ||
		(!r.FromWake && next.FromWake && r.From == "") || later(r.From, next.From) || earlier(r.Until, next.Until)
}

// OutsideAt is the rule in force at an instant: a looser rule waiting for
// its day replaces the current one from that local date.
func (c Config) OutsideAt(at time.Time) OutsideRule {
	if c.OutsideNext == nil || c.OutsideNextFrom == "" {
		return c.Outside
	}
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		location = time.UTC
	}
	if at.In(location).Format("2006-01-02") >= c.OutsideNextFrom {
		return *c.OutsideNext
	}
	return c.Outside
}

// outsideHolds reports whether the rule holds at this instant: enabled,
// not met today, and inside its hours.
func outsideHolds(rule OutsideRule, facts Facts, location *time.Location, at time.Time) bool {
	if !rule.Enabled || len(rule.Holds) == 0 || facts.OutsideMinutes >= float64(rule.Minutes) {
		return false
	}
	local := at.In(location)
	clock := local.Format("15:04")
	if rule.Until != "" && clock >= rule.Until {
		return false
	}
	if rule.From != "" && clock < rule.From {
		return false
	}
	if rule.FromWake && rule.From == "" {
		// From waking today; with no known wake it does not start, so
		// missing evidence never locks the night.
		if facts.WakeAt == nil || at.Before(*facts.WakeAt) || facts.WakeAt.In(location).Format("2006-01-02") != local.Format("2006-01-02") {
			return false
		}
	}
	return true
}

// WithOutside sets the outside rule as of now. A rule that asks less than
// the one in force waits for the next local day, and today keeps the rule
// in force; one that asks the same or more applies at once. Setting the rule
// in force again also settles a waiting rule whose day has come.
func (c Config) WithOutside(next OutsideRule, now time.Time) Config {
	current := c.OutsideAt(now)
	if c.OutsideNext != nil && reflect.DeepEqual(current, *c.OutsideNext) && reflect.DeepEqual(next, current) {
		c.Outside, c.OutsideNext, c.OutsideNextFrom = current, nil, ""
		return c
	}
	if current.Looser(next) {
		location, err := time.LoadLocation(c.Timezone)
		if err != nil {
			location = time.UTC
		}
		c.Outside, c.OutsideNext = current, &next
		c.OutsideNextFrom = now.In(location).AddDate(0, 0, 1).Format("2006-01-02")
		return c
	}
	if reflect.DeepEqual(next, current) && c.OutsideNext != nil {
		// Nothing asked of the rule: a looser one keeps waiting.
		return c
	}
	c.Outside, c.OutsideNext, c.OutsideNextFrom = next, nil, ""
	return c
}
