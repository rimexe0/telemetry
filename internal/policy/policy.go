// Package policy decides, deterministically, which mode the owner is in and
// what is blocked. It has no storage or network dependency so the same rules
// can run on the server, in the simulator and in local device agents.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ModeEmergency = "emergency"
	ModeGate      = "gate"
	ModeUrgent    = "urgent"
	ModeRecovery  = "recovery"
	ModeWork      = "work"
	ModeBreak     = "break"
	ModePlanGate  = "plan_gate"
	ModeLeisure   = "leisure"

	BlockKindWork = "work"
	BlockKindFree = "free"
)

var ErrInvalidConfig = errors.New("invalid policy config")

// TargetGroup names one distraction or essential and how it appears on each
// platform. Identifiers are owner-configured; nothing is seeded.
type TargetGroup struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	AndroidPackages []string `json:"android_packages,omitempty"`
	MacApps         []string `json:"mac_apps,omitempty"`
	Domains         []string `json:"domains,omitempty"`
}

type PlanGateConfig struct {
	Enabled bool `json:"enabled"`
	// EndsAt is an optional local "HH:MM" after which a missing plan no
	// longer blocks anything that day.
	EndsAt string `json:"ends_at,omitempty"`
}

type Config struct {
	Timezone         string         `json:"timezone"`
	Distractions     []TargetGroup  `json:"distractions"`
	Essentials       []TargetGroup  `json:"essentials"`
	PlanGate         PlanGateConfig `json:"plan_gate"`
	EmergencyMinutes int            `json:"emergency_minutes"`
}

// Block is a plan block reduced to what the policy needs. Work blocks
// constrain distractions while open; free blocks and finished blocks do not.
type Block struct {
	ID    int64     `json:"id"`
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Kind  string    `json:"kind"`
	State string    `json:"state"`
}

type Execution struct {
	State    string `json:"state"`
	Recovery bool   `json:"recovery"`
	Title    string `json:"title,omitempty"`
}

// Gate locks everything except essentials from ActiveFrom until it is
// satisfied or Until passes. Until is mandatory so that missing or stale
// evidence can never keep a lock forever.
type Gate struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	ActiveFrom time.Time `json:"active_from"`
	Until      time.Time `json:"until"`
	Satisfied  bool      `json:"satisfied"`
}

type Urgent struct {
	Purpose         string    `json:"purpose"`
	AllowedGroupIDs []string  `json:"allowed_group_ids,omitempty"`
	Until           time.Time `json:"until"`
}

type Emergency struct {
	StartedAt time.Time `json:"started_at"`
	Until     time.Time `json:"until"`
}

type Facts struct {
	PlanExists bool       `json:"plan_exists"`
	PlanDate   string     `json:"plan_date,omitempty"`
	Blocks     []Block    `json:"blocks,omitempty"`
	Execution  *Execution `json:"execution,omitempty"`
	Gates      []Gate     `json:"gates,omitempty"`
	Urgent     *Urgent    `json:"urgent,omitempty"`
	Emergency  *Emergency `json:"emergency,omitempty"`
	BreakSince *time.Time `json:"break_since,omitempty"`
}

type Targets struct {
	AndroidPackages []string `json:"android_packages"`
	MacApps         []string `json:"mac_apps"`
	Domains         []string `json:"domains"`
}

type Decision struct {
	At            time.Time `json:"at"`
	Mode          string    `json:"mode"`
	Reason        string    `json:"reason"`
	PolicyVersion int64     `json:"policy_version"`
	// BlockAll means everything except essentials is blocked (gates).
	BlockAll      bool     `json:"block_all"`
	BlockedGroups []string `json:"blocked_groups"`
	Blocked       Targets  `json:"blocked"`
	AlwaysAllowed Targets  `json:"always_allowed"`
	Basis         []string `json:"basis"`
	Warnings      []string `json:"warnings,omitempty"`
}

var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Normalize fills technical defaults and validates the config.
func (c *Config) Normalize() error {
	if c.Timezone == "" {
		c.Timezone = "Europe/Istanbul"
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("%w: timezone %q is not recognised", ErrInvalidConfig, c.Timezone)
	}
	if c.EmergencyMinutes == 0 {
		c.EmergencyMinutes = 10
	}
	if c.EmergencyMinutes < 1 || c.EmergencyMinutes > 120 {
		return fmt.Errorf("%w: emergency_minutes must be between 1 and 120", ErrInvalidConfig)
	}
	if c.PlanGate.EndsAt != "" && !clockPattern.MatchString(c.PlanGate.EndsAt) {
		return fmt.Errorf("%w: plan_gate.ends_at must be HH:MM", ErrInvalidConfig)
	}
	total := 0
	for _, list := range [][]TargetGroup{c.Distractions, c.Essentials} {
		// An id may appear in both lists; the essential entry wins at runtime
		// and the decision reports the conflict.
		seen := map[string]bool{}
		total += len(list)
		for i := range list {
			group := &list[i]
			group.ID = strings.TrimSpace(group.ID)
			if group.ID == "" || len(group.ID) > 64 {
				return fmt.Errorf("%w: every target group needs an id of at most 64 characters", ErrInvalidConfig)
			}
			if seen[group.ID] {
				return fmt.Errorf("%w: target group id %q is used twice", ErrInvalidConfig, group.ID)
			}
			seen[group.ID] = true
			for j, domain := range group.Domains {
				domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
				if domain == "" || strings.ContainsAny(domain, "/:@ ") {
					return fmt.Errorf("%w: group %q domain %q must be a host name", ErrInvalidConfig, group.ID, group.Domains[j])
				}
				group.Domains[j] = domain
			}
		}
	}
	if total > 500 {
		return fmt.Errorf("%w: at most 500 target groups are supported", ErrInvalidConfig)
	}
	return nil
}

// Validate rejects facts that could lock the owner without a bound.
func (f Facts) Validate() error {
	for _, gate := range f.Gates {
		if gate.Until.IsZero() || !gate.Until.After(gate.ActiveFrom) {
			return fmt.Errorf("gate %q needs an end after its start so missing evidence cannot lock indefinitely", gate.ID)
		}
	}
	if f.Urgent != nil && f.Urgent.Until.IsZero() {
		return errors.New("urgent purpose needs an expiry")
	}
	if f.Emergency != nil && !f.Emergency.Until.After(f.Emergency.StartedAt) {
		return errors.New("emergency needs an end after its start")
	}
	return nil
}

// Evaluate returns the decision at one instant.
func Evaluate(config Config, version int64, facts Facts, at time.Time) Decision {
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		location = time.UTC
	}
	decision := Decision{At: at.UTC(), PolicyVersion: version, BlockedGroups: []string{}, Basis: []string{}}
	essential := map[string]bool{}
	for _, group := range config.Essentials {
		essential[group.ID] = true
	}
	decision.AlwaysAllowed = expand(config.Essentials, nil)
	blockDistractions := func(except map[string]bool) {
		for _, group := range config.Distractions {
			switch {
			case essential[group.ID]:
				decision.Warnings = append(decision.Warnings, fmt.Sprintf("%s is listed as essential and is not blocked", group.ID))
			case except[group.ID]:
			default:
				decision.BlockedGroups = append(decision.BlockedGroups, group.ID)
			}
		}
		decision.Blocked = expand(config.Distractions, decision.BlockedGroups)
		decision.Blocked = subtract(decision.Blocked, decision.AlwaysAllowed)
	}
	clock := func(t time.Time) string { return t.In(location).Format("15:04") }

	if emergency := facts.Emergency; emergency != nil && !at.Before(emergency.StartedAt) && at.Before(emergency.Until) {
		decision.Mode = ModeEmergency
		decision.Reason = "Emergency unlock until " + clock(emergency.Until) + ". What you do now will be reviewed afterwards."
		decision.Basis = append(decision.Basis, "emergency")
		decision.Blocked = emptyTargets()
		return decision
	}
	for _, gate := range facts.Gates {
		if gate.Satisfied || at.Before(gate.ActiveFrom) || !at.Before(gate.Until) {
			continue
		}
		decision.Mode = ModeGate
		decision.BlockAll = true
		decision.Reason = fmt.Sprintf("%s. Calls and essentials stay available. Unlocks when done, at the latest %s.", gate.Label, clock(gate.Until))
		decision.Basis = append(decision.Basis, "gate:"+gate.ID)
		blockDistractions(nil)
		return decision
	}
	if urgent := facts.Urgent; urgent != nil && at.Before(urgent.Until) {
		allowed := map[string]bool{}
		for _, id := range urgent.AllowedGroupIDs {
			allowed[id] = true
		}
		decision.Mode = ModeUrgent
		decision.Reason = "Urgent: " + urgent.Purpose + " (until " + clock(urgent.Until) + "). Only what this needs is opened up."
		decision.Basis = append(decision.Basis, "urgent")
		blockDistractions(allowed)
		return decision
	}
	if execution := facts.Execution; execution != nil && execution.Recovery {
		decision.Mode = ModeRecovery
		decision.Reason = "Offline recovery: step away from screens. Distractions stay closed."
		decision.Basis = append(decision.Basis, "execution:recovery")
		blockDistractions(nil)
		return decision
	}
	if block := openWorkBlock(facts.Blocks, at); block != nil || executionActive(facts.Execution) {
		decision.Mode = ModeWork
		title := ""
		if block != nil {
			title = block.Title
			decision.Basis = append(decision.Basis, fmt.Sprintf("plan_block:%d", block.ID))
		}
		if facts.Execution != nil && executionActive(facts.Execution) {
			decision.Basis = append(decision.Basis, "execution:"+facts.Execution.State)
			if title == "" {
				title = facts.Execution.Title
			}
		}
		if title == "" {
			title = "your current task"
		}
		if facts.BreakSince != nil && !at.Before(*facts.BreakSince) {
			decision.Mode = ModeBreak
			decision.Reason = "Break from " + title + ". Rest away from the feed; it stays closed."
		} else {
			decision.Reason = "Working on " + title + "."
			if block != nil {
				decision.Reason += " Until " + clock(block.End) + "."
			}
		}
		blockDistractions(nil)
		return decision
	}
	if config.PlanGate.Enabled && !facts.PlanExists && !planGateEnded(config, location, at) {
		decision.Mode = ModePlanGate
		decision.Reason = "No plan for today yet. Plan your day to open everything up."
		decision.Basis = append(decision.Basis, "plan_gate")
		blockDistractions(nil)
		return decision
	}
	decision.Mode = ModeLeisure
	decision.Reason = "Free time. Nothing is blocked."
	decision.Blocked = emptyTargets()
	return decision
}

func openWorkBlock(blocks []Block, at time.Time) *Block {
	for i := range blocks {
		block := &blocks[i]
		if block.Kind != BlockKindWork || at.Before(block.Start) || !at.Before(block.End) {
			continue
		}
		switch block.State {
		case "done", "skipped", "switched":
			continue
		}
		return block
	}
	return nil
}

func executionActive(execution *Execution) bool {
	return execution != nil && (execution.State == "active" || execution.State == "waiting_external")
}

func planGateEnded(config Config, location *time.Location, at time.Time) bool {
	if config.PlanGate.EndsAt == "" {
		return false
	}
	local := at.In(location)
	return local.Format("15:04") >= config.PlanGate.EndsAt
}

func emptyTargets() Targets {
	return Targets{AndroidPackages: []string{}, MacApps: []string{}, Domains: []string{}}
}

func expand(groups []TargetGroup, only []string) Targets {
	include := map[string]bool{}
	for _, id := range only {
		include[id] = true
	}
	result := emptyTargets()
	for _, group := range groups {
		if only != nil && !include[group.ID] {
			continue
		}
		result.AndroidPackages = append(result.AndroidPackages, group.AndroidPackages...)
		result.MacApps = append(result.MacApps, group.MacApps...)
		result.Domains = append(result.Domains, group.Domains...)
	}
	result.AndroidPackages = uniqueSorted(result.AndroidPackages)
	result.MacApps = uniqueSorted(result.MacApps)
	result.Domains = uniqueSorted(result.Domains)
	return result
}

// subtract removes identifiers that are also essential, so an identifier that
// appears in both lists is never blocked.
func subtract(blocked, allowed Targets) Targets {
	filter := func(values, remove []string) []string {
		drop := map[string]bool{}
		for _, value := range remove {
			drop[value] = true
		}
		out := []string{}
		for _, value := range values {
			if !drop[value] {
				out = append(out, value)
			}
		}
		return out
	}
	return Targets{
		AndroidPackages: filter(blocked.AndroidPackages, allowed.AndroidPackages),
		MacApps:         filter(blocked.MacApps, allowed.MacApps),
		Domains:         filter(blocked.Domains, allowed.Domains),
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
