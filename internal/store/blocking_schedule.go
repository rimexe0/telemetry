package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/policy"
)

const (
	AgentChangeSchedule = "schedule"
	AgentChangeOpening  = "opening"
)

// ScheduleChange changes when distractions are closed. Fields left out stay
// as they are.
type ScheduleChange struct {
	ClosedByDefault  *bool            `json:"closed_by_default,omitempty"`
	DistractionTimes *[]policy.Window `json:"distraction_times,omitempty"`
	// OpenAddresses replaces the whole list; Add and Remove edit it.
	OpenAddresses       *[]string `json:"open_addresses,omitempty"`
	AddOpenAddresses    []string  `json:"add_open_addresses,omitempty"`
	RemoveOpenAddresses []string  `json:"remove_open_addresses,omitempty"`
	// LockUntilPlanned closes everything but the essentials from when the
	// owner wakes until the day has a plan.
	LockUntilPlanned *bool `json:"lock_until_planned,omitempty"`
	// Outside replaces the outside rule. One that asks less in any way
	// takes effect the next day; one that asks more, at once.
	Outside *policy.OutsideRule `json:"outside,omitempty"`
	// OpeningRule and Morning replace those rules; a looser one waits for
	// the next day in the same way.
	OpeningRule *policy.OpeningRule `json:"opening_rule,omitempty"`
	Morning     *policy.MorningRule `json:"morning,omitempty"`
}

// applyScheduleTx writes a new policy version and returns the change that
// restores the schedule it replaced.
func applyScheduleTx(ctx context.Context, tx pgx.Tx, change ScheduleChange, note string) (ScheduleChange, policy.Config, int64, error) {
	if _, err := tx.Exec(ctx, `LOCK TABLE policy_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return ScheduleChange{}, policy.Config{}, 0, err
	}
	config := policy.Config{}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT config FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ScheduleChange{}, config, 0, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return ScheduleChange{}, config, 0, err
		}
	}
	closed, times, addresses := config.ClosedByDefault, append([]policy.Window{}, config.DistractionTimes...), append([]string{}, config.OpenAddresses...)
	locked := config.PlanGate.FromWake
	now := time.Now().UTC()
	outside := config.OutsideAt(now)
	previous := ScheduleChange{ClosedByDefault: &closed, DistractionTimes: &times, OpenAddresses: &addresses, LockUntilPlanned: &locked}
	if change.Outside != nil {
		// Only a change to the rule restores it when reverted.
		previous.Outside = &outside
		next := *change.Outside
		next.Holds = append([]string{}, next.Holds...)
		if err := next.Normalize(); err != nil {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
		}
		config = config.WithOutside(next, now)
	} else {
		config = config.WithOutside(outside, now)
	}
	openingRule, morning := config.OpeningRuleAt(now), config.MorningAt(now)
	if change.OpeningRule != nil {
		previous.OpeningRule = &openingRule
		if err := change.OpeningRule.Normalize(); err != nil {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
		}
		openingRule = *change.OpeningRule
	}
	config = config.WithOpeningRule(openingRule, now)
	if change.Morning != nil {
		previous.Morning = &morning
		if err := change.Morning.Normalize(); err != nil {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
		}
		morning = *change.Morning
	}
	config = config.WithMorning(morning, now)
	if change.LockUntilPlanned != nil {
		if *change.LockUntilPlanned && !config.PlanGate.FromWake {
			since := time.Now().UTC()
			config.PlanGate.Since = &since
		}
		if !*change.LockUntilPlanned {
			config.PlanGate.Since = nil
		}
		config.PlanGate.FromWake, config.PlanGate.LockAll = *change.LockUntilPlanned, *change.LockUntilPlanned
	}
	if change.ClosedByDefault != nil {
		config.ClosedByDefault = *change.ClosedByDefault
	}
	if change.DistractionTimes != nil {
		config.DistractionTimes = *change.DistractionTimes
	}
	if change.OpenAddresses != nil {
		config.OpenAddresses = *change.OpenAddresses
	}
	for _, address := range change.AddOpenAddresses {
		address = policy.NormalizeAddress(address)
		present := false
		for _, existing := range config.OpenAddresses {
			present = present || existing == address
		}
		if !present {
			config.OpenAddresses = append(config.OpenAddresses, address)
		}
	}
	for _, address := range change.RemoveOpenAddresses {
		address = policy.NormalizeAddress(address)
		kept := []string{}
		for _, existing := range config.OpenAddresses {
			if existing != address {
				kept = append(kept, existing)
			}
		}
		config.OpenAddresses = kept
	}
	if err := config.Normalize(); err != nil {
		return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return ScheduleChange{}, config, 0, err
	}
	if runes := []rune(note); len(runes) > maxPlanningTextRunes {
		note = string(runes[:maxPlanningTextRunes])
	}
	var version int64
	err = tx.QueryRow(ctx, `INSERT INTO policy_versions (config, note) VALUES ($1::jsonb, $2) RETURNING id`, string(encoded), note).Scan(&version)
	return previous, config, version, err
}

// ApplyBlockingSchedule changes when distractions are closed and logs it.
func (p *Postgres) ApplyBlockingSchedule(ctx context.Context, change ScheduleChange, reason string) (AgentChange, policy.Config, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, policy.Config{}, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, policy.Config{}, err
	}
	defer tx.Rollback(ctx)
	previous, config, version, err := applyScheduleTx(ctx, tx, change, reason)
	if err != nil {
		return AgentChange{}, policy.Config{}, err
	}
	logged, err := insertAgentChange(ctx, tx, AgentChangeSchedule, change, previous, reason, &version, nil)
	if err != nil {
		return AgentChange{}, policy.Config{}, err
	}
	return logged, config, tx.Commit(ctx)
}

// Opening lifts the block on named sites and apps, or on every distraction,
// for a stated purpose from StartsAt until a time.
type Opening struct {
	ID         int64      `json:"id"`
	Purpose    string     `json:"purpose"`
	Sites      []string   `json:"sites"`
	Apps       []string   `json:"apps"`
	Everything bool       `json:"everything"`
	StartsAt   time.Time  `json:"starts_at"`
	Until      time.Time  `json:"until"`
	CreatedAt  time.Time  `json:"created_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}

const openingColumns = `id, purpose, sites, apps, everything, starts_at, until, created_at, ended_at`

// ErrOpeningRefused is an opening the owner's opening rule does not allow.
var ErrOpeningRefused = errors.New("opening refused")

func scanOpening(row pgx.Row) (Opening, error) {
	var opening Opening
	err := row.Scan(&opening.ID, &opening.Purpose, &opening.Sites, &opening.Apps, &opening.Everything, &opening.StartsAt, &opening.Until, &opening.CreatedAt, &opening.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return opening, ErrNotFound
	}
	return opening, err
}

// OpenNow records an opening and logs it; reverting the change ends it.
// Until is read as the length asked for from now. Under the owner's opening
// rule the opening starts after the wait and keeps that length, a length
// over the limit or an opening over the day's count is refused.
func (p *Postgres) OpenNow(ctx context.Context, opening Opening, reason string) (AgentChange, Opening, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	opening.Purpose = strings.TrimSpace(opening.Purpose)
	clean := func(values []string) []string {
		result := []string{}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
		return result
	}
	opening.Sites, opening.Apps = clean(opening.Sites), clean(opening.Apps)
	for i, site := range opening.Sites {
		opening.Sites[i], _, _ = strings.Cut(policy.NormalizeAddress(site), "/")
	}
	switch {
	case opening.Purpose == "" || len([]rune(opening.Purpose)) > 300:
		return AgentChange{}, Opening{}, fmt.Errorf("%w: a purpose of at most 300 characters is required", ErrInvalidAgentChange)
	case !opening.Everything && len(opening.Sites)+len(opening.Apps) == 0:
		return AgentChange{}, Opening{}, fmt.Errorf("%w: name the sites or apps to open, or open everything", ErrInvalidAgentChange)
	case len(opening.Sites)+len(opening.Apps) > 50:
		return AgentChange{}, Opening{}, fmt.Errorf("%w: at most 50 sites and apps", ErrInvalidAgentChange)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	defer tx.Rollback(ctx)
	// One opening at a time is checked against the day's count.
	if _, err := tx.Exec(ctx, `LOCK TABLE openings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return AgentChange{}, Opening{}, err
	}
	config := policy.Config{}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT config FROM policy_versions ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AgentChange{}, Opening{}, err
	}
	if raw != nil {
		if err := json.Unmarshal(raw, &config); err != nil {
			return AgentChange{}, Opening{}, err
		}
	}
	if err := config.Normalize(); err != nil {
		return AgentChange{}, Opening{}, err
	}
	now := time.Now().UTC()
	rule := config.OpeningRuleAt(now)
	length := opening.Until.Sub(now).Round(time.Minute)
	if rule.MaxMinutes > 0 && length > time.Duration(rule.MaxMinutes)*time.Minute {
		return AgentChange{}, Opening{}, fmt.Errorf("%w: an opening lasts at most %d minutes", ErrOpeningRefused, rule.MaxMinutes)
	}
	if rule.PerDay > 0 {
		location, err := time.LoadLocation(config.Timezone)
		if err != nil {
			return AgentChange{}, Opening{}, err
		}
		local := now.In(location)
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		used, err := openingsSince(ctx, tx, dayStart)
		if err != nil {
			return AgentChange{}, Opening{}, err
		}
		if used >= rule.PerDay {
			return AgentChange{}, Opening{}, fmt.Errorf("%w: all %d openings for today are used", ErrOpeningRefused, rule.PerDay)
		}
	}
	opening.StartsAt = now.Add(time.Duration(rule.WaitMinutes) * time.Minute)
	opening.Until = opening.StartsAt.Add(length)
	saved, err := scanOpening(tx.QueryRow(ctx, `INSERT INTO openings (purpose, sites, apps, everything, starts_at, until) VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+openingColumns,
		opening.Purpose, opening.Sites, opening.Apps, opening.Everything, opening.StartsAt, opening.Until.UTC()))
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	logged, err := insertAgentChange(ctx, tx, AgentChangeOpening, saved, map[string]int64{"end_opening": saved.ID}, reason, nil, nil)
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	return logged, saved, tx.Commit(ctx)
}

// openingsSince counts the openings made since a time, leaving out those
// ended before they started.
func openingsSince(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, since time.Time) (int, error) {
	var used int
	err := q.QueryRow(ctx, `SELECT count(*) FROM openings WHERE created_at >= $1 AND (ended_at IS NULL OR ended_at > starts_at)`, since).Scan(&used)
	return used, err
}

// OpeningsSince counts the openings made since a time that count toward the
// day's limit.
func (p *Postgres) OpeningsSince(ctx context.Context, since time.Time) (int, error) {
	return openingsSince(ctx, p.pool, since)
}

// EndOpening ends an opening now, or before it starts. Ending one that has
// ended already changes nothing.
func (p *Postgres) EndOpening(ctx context.Context, id int64) (Opening, error) {
	return scanOpening(p.pool.QueryRow(ctx, `UPDATE openings SET ended_at = COALESCE(ended_at, now()) WHERE id = $1 RETURNING `+openingColumns, id))
}

// ActiveOpenings returns the openings in force at a time and those waiting
// to start.
func (p *Postgres) ActiveOpenings(ctx context.Context, at time.Time) ([]Opening, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+openingColumns+` FROM openings WHERE until > $1 AND ended_at IS NULL ORDER BY id LIMIT 50`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	openings := []Opening{}
	for rows.Next() {
		opening, err := scanOpening(rows)
		if err != nil {
			return nil, err
		}
		openings = append(openings, opening)
	}
	return openings, rows.Err()
}

func revertOpening(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (any, any, error) {
	var restore map[string]int64
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	ended, err := scanOpening(tx.QueryRow(ctx, `UPDATE openings SET ended_at = COALESCE(ended_at, now()) WHERE id = $1 RETURNING `+openingColumns, restore["end_opening"]))
	if err != nil {
		return nil, nil, err
	}
	// An ended opening is not reopened by reverting again.
	return ended, map[string]int64{}, nil
}

// PolicyOpenings turns stored openings into the groups they open under config.
func PolicyOpenings(config policy.Config, openings []Opening) []policy.Opening {
	result := []policy.Opening{}
	for _, opening := range openings {
		item := policy.Opening{ID: opening.ID, Purpose: opening.Purpose, All: opening.Everything, From: opening.StartsAt, Until: opening.Until}
		for _, group := range config.Distractions {
			matched := false
			for _, domain := range group.Domains {
				for _, site := range opening.Sites {
					matched = matched || site == domain || strings.HasSuffix(site, "."+domain) || strings.HasSuffix(domain, "."+site)
				}
			}
			for _, app := range opening.Apps {
				for _, name := range append(append([]string{}, group.AndroidPackages...), group.MacApps...) {
					matched = matched || strings.EqualFold(app, name)
				}
			}
			if matched {
				item.GroupIDs = append(item.GroupIDs, group.ID)
			}
		}
		result = append(result, item)
	}
	return result
}
