package policy

import (
	"fmt"
	"time"
)

// ExtensionRule hides a browser whose Telemetry extension has not checked
// in for QuietSeconds while sites are blocked, without quitting it, so
// switching the extension off does not get around site blocking (owner
// request 2026-10-30).
type ExtensionRule struct {
	Enabled      bool `json:"enabled"`
	QuietSeconds int  `json:"quiet_seconds,omitempty"`
}

func (r ExtensionRule) Normalize() error {
	if r.Enabled && (r.QuietSeconds < 30 || r.QuietSeconds > 3600) {
		return fmt.Errorf("%w: extension.quiet_seconds must be between 30 and 3600", ErrInvalidConfig)
	}
	return nil
}

// Looser reports whether next asks less: off, or a longer quiet time.
func (r ExtensionRule) Looser(next ExtensionRule) bool {
	return r.Enabled && (!next.Enabled || next.QuietSeconds > r.QuietSeconds)
}

// SwitchesOff reports whether next turns off a rule that is on. Once on, the
// rule cannot be switched off from the panel or by an agent, not even the
// next day: switching it off was how site blocking got bypassed (owner
// request 2026-11-22). A longer quiet time still waits for the next day.
func (r ExtensionRule) SwitchesOff(next ExtensionRule) bool {
	return r.Enabled && !next.Enabled
}

// ErrExtensionStaysOn refuses switching the extension rule off.
var ErrExtensionStaysOn = fmt.Errorf("%w: the browser extension rule stays on once it is on; it cannot be switched off", ErrInvalidConfig)

// ExtensionRuleAt is the extension rule in force at an instant.
func (c Config) ExtensionRuleAt(at time.Time) ExtensionRule {
	return ruleAt(c.Timezone, c.Extension, c.ExtensionNext, c.ExtensionNextFrom, at)
}

// WithExtensionRule sets the rule as of now; a looser one waits for the
// next day (see NextDay). Callers refuse SwitchesOff first.
func (c Config) WithExtensionRule(next ExtensionRule, now time.Time) Config {
	c.Extension, c.ExtensionNext, c.ExtensionNextFrom = withRule(c.Timezone, c.Extension, c.ExtensionNext, c.ExtensionNextFrom, next, ExtensionRule.Looser, now, c.NextDay(now))
	return c
}

// withExtension asks desktops to hide browsers without a working extension
// while the decision blocks sites.
func withExtension(config Config, decision Decision, at time.Time) Decision {
	rule := config.ExtensionRuleAt(at)
	if rule.Enabled && (decision.BlockAll || len(decision.Blocked.Domains) > 0) {
		decision.RequireExtensionSeconds = rule.QuietSeconds
	}
	return decision
}
