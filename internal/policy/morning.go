package policy

import (
	"fmt"
	"time"
)

// ModeMorning closes everything but the essentials, or the distractions,
// each morning until the owner scans the printed morning code (owner request
// 2026-10-26).
const ModeMorning = "morning"

// MorningRule holds the day shut from waking, or from a set time, until the
// morning code is scanned. Until is required, so the hold always ends that
// day even if the code is lost or the scan never arrives.
type MorningRule struct {
	Enabled bool `json:"enabled"`
	// FromWake starts the hold when the owner wakes; From starts it at a
	// local "HH:MM" instead.
	FromWake bool   `json:"from_wake,omitempty"`
	From     string `json:"from,omitempty"`
	// Until is a local "HH:MM" after which the hold gives up for the day.
	Until string `json:"until"`
	// LockAll closes everything but the essentials; otherwise only the
	// distractions.
	LockAll bool `json:"lock_all,omitempty"`
}

// Normalize validates a rule. A disabled rule keeps its settings so it can
// be switched back on as it was.
func (r MorningRule) Normalize() error {
	if !r.Enabled {
		return nil
	}
	switch {
	case !r.FromWake && r.From == "":
		return fmt.Errorf("%w: morning needs from_wake or from", ErrInvalidConfig)
	case r.From != "" && !clockPattern.MatchString(r.From):
		return fmt.Errorf("%w: morning.from must be HH:MM", ErrInvalidConfig)
	case !clockPattern.MatchString(r.Until):
		return fmt.Errorf("%w: morning.until must be HH:MM; the hold always ends that day", ErrInvalidConfig)
	case r.From != "" && r.Until <= r.From:
		return fmt.Errorf("%w: morning.until must be after morning.from", ErrInvalidConfig)
	}
	return nil
}

// Looser reports whether next asks less of the owner than r in any way.
func (r MorningRule) Looser(next MorningRule) bool {
	if !r.Enabled {
		return false
	}
	if !next.Enabled {
		return true
	}
	// A later fixed start asks less. Switching between waking and a fixed
	// start may start later on some days, so it waits too.
	laterStart := r.From != next.From && (r.From == "" || next.From == "" || next.From > r.From)
	return (r.LockAll && !next.LockAll) || next.Until < r.Until || laterStart
}

// MorningAt is the morning rule in force at an instant.
func (c Config) MorningAt(at time.Time) MorningRule {
	return ruleAt(c.Timezone, c.Morning, c.MorningNext, c.MorningNextFrom, at)
}

// WithMorning sets the morning rule as of now; a looser one waits for the
// next local day.
func (c Config) WithMorning(next MorningRule, now time.Time) Config {
	c.Morning, c.MorningNext, c.MorningNextFrom = withRule(c.Timezone, c.Morning, c.MorningNext, c.MorningNextFrom, next, MorningRule.Looser, now)
	return c
}

// MorningStart is when today's morning hold starts at this instant, if it
// has started and not given up. With a start from waking and no known wake
// it does not start, so missing evidence never locks anything.
func MorningStart(rule MorningRule, wake *time.Time, location *time.Location, at time.Time) (time.Time, bool) {
	if !rule.Enabled {
		return time.Time{}, false
	}
	local := at.In(location)
	day := local.Format("2006-01-02")
	until, err := time.ParseInLocation("2006-01-02 15:04", day+" "+rule.Until, location)
	if err != nil || !at.Before(until) {
		return time.Time{}, false
	}
	if rule.From != "" {
		start, err := time.ParseInLocation("2006-01-02 15:04", day+" "+rule.From, location)
		return start, err == nil && !at.Before(start)
	}
	if wake == nil || at.Before(*wake) || wake.In(location).Format("2006-01-02") != day {
		return time.Time{}, false
	}
	return *wake, true
}

// morningHolds reports whether the morning hold applies: started today,
// before its end, and no scan since it started.
func morningHolds(rule MorningRule, facts Facts, location *time.Location, at time.Time) bool {
	start, started := MorningStart(rule, facts.WakeAt, location, at)
	if !started {
		return false
	}
	return facts.MorningScannedAt == nil || facts.MorningScannedAt.Before(start) || facts.MorningScannedAt.After(at)
}
