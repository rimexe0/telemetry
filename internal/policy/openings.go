package policy

import (
	"fmt"
	"time"
)

// OpeningRule sets how an opening works (owner request 2026-10-26): it
// starts only after a wait, lasts at most a set time, and only so many are
// made a day. Zero leaves that part out. Every number is the owner's to set.
type OpeningRule struct {
	// WaitMinutes is how long after it is asked for an opening starts.
	WaitMinutes int `json:"wait_minutes,omitempty"`
	// MaxMinutes is the longest an opening may last.
	MaxMinutes int `json:"max_minutes,omitempty"`
	// PerDay is how many openings may be made each local day. One ended
	// before it started does not count.
	PerDay int `json:"per_day,omitempty"`
}

func (r OpeningRule) Normalize() error {
	switch {
	case r.WaitMinutes < 0 || r.WaitMinutes > 240:
		return fmt.Errorf("%w: opening_rule.wait_minutes must be between 0 and 240", ErrInvalidConfig)
	case r.MaxMinutes < 0 || r.MaxMinutes > 1440:
		return fmt.Errorf("%w: opening_rule.max_minutes must be between 0 and 1440", ErrInvalidConfig)
	case r.PerDay < 0 || r.PerDay > 50:
		return fmt.Errorf("%w: opening_rule.per_day must be between 0 and 50", ErrInvalidConfig)
	}
	return nil
}

// Looser reports whether next asks less of the owner than r in any way: a
// shorter wait, or a longer or removed limit.
func (r OpeningRule) Looser(next OpeningRule) bool {
	raised := func(limit, next int) bool { return limit > 0 && (next == 0 || next > limit) }
	return next.WaitMinutes < r.WaitMinutes || raised(r.MaxMinutes, next.MaxMinutes) || raised(r.PerDay, next.PerDay)
}

// OpeningRuleAt is the opening rule in force at an instant.
func (c Config) OpeningRuleAt(at time.Time) OpeningRule {
	return ruleAt(c.Timezone, c.OpeningRule, c.OpeningRuleNext, c.OpeningRuleNextFrom, at)
}

// WithOpeningRule sets the opening rule as of now; a looser one waits for
// the next local day.
func (c Config) WithOpeningRule(next OpeningRule, now time.Time) Config {
	c.OpeningRule, c.OpeningRuleNext, c.OpeningRuleNextFrom = withRule(c.Timezone, c.OpeningRule, c.OpeningRuleNext, c.OpeningRuleNextFrom, next, OpeningRule.Looser, now)
	return c
}
