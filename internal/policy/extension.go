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

// ExtensionRuleAt is the extension rule in force at an instant.
func (c Config) ExtensionRuleAt(at time.Time) ExtensionRule {
	return ruleAt(c.Timezone, c.Extension, c.ExtensionNext, c.ExtensionNextFrom, at)
}

// WithExtensionRule sets the rule as of now; a looser one waits for the
// next local day.
func (c Config) WithExtensionRule(next ExtensionRule, now time.Time) Config {
	c.Extension, c.ExtensionNext, c.ExtensionNextFrom = withRule(c.Timezone, c.Extension, c.ExtensionNext, c.ExtensionNextFrom, next, ExtensionRule.Looser, now)
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
