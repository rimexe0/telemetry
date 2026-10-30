package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	// SettingsLock and Bedtime replace those rules in the same way.
	SettingsLock *policy.SettingsLock `json:"settings_lock,omitempty"`
	Bedtime      *policy.Bedtime      `json:"bedtime,omitempty"`
}

// windowMinutes marks each minute of the day the windows cover.
func windowMinutes(windows []policy.Window) [1440]bool {
	var minutes [1440]bool
	for _, window := range windows {
		start, err1 := time.Parse("15:04", window.Start)
		end, err2 := time.Parse("15:04", window.End)
		if err1 != nil || err2 != nil {
			continue
		}
		from, to := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
		for minute := from; minute != to; minute = (minute + 1) % 1440 {
			minutes[minute] = true
		}
	}
	return minutes
}

// scheduleLoosens reports whether a change asks less of the owner than the
// config in force in any way.
func scheduleLoosens(config policy.Config, change ScheduleChange, now time.Time) bool {
	if change.ClosedByDefault != nil && !*change.ClosedByDefault && config.ClosedByDefault {
		return true
	}
	if change.DistractionTimes != nil {
		before, after := windowMinutes(config.DistractionTimes), windowMinutes(*change.DistractionTimes)
		for minute := range after {
			if after[minute] && !before[minute] {
				return true
			}
		}
	}
	if len(change.AddOpenAddresses) > 0 {
		return true
	}
	if change.OpenAddresses != nil {
		for _, address := range *change.OpenAddresses {
			if !slices.Contains(config.OpenAddresses, policy.NormalizeAddress(address)) {
				return true
			}
		}
	}
	if change.LockUntilPlanned != nil && !*change.LockUntilPlanned && config.PlanGate.FromWake {
		return true
	}
	return change.Outside != nil && config.OutsideAt(now).Looser(*change.Outside) ||
		change.OpeningRule != nil && config.OpeningRuleAt(now).Looser(*change.OpeningRule) ||
		change.Morning != nil && config.MorningAt(now).Looser(*change.Morning) ||
		change.SettingsLock != nil && config.SettingsLockAt(now).Looser(*change.SettingsLock) ||
		change.Bedtime != nil && config.BedtimeAt(now).Looser(*change.Bedtime)
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
	now := time.Now().UTC()
	// Loosening is refused while the settings lock holds; after the PIN,
	// changes apply at once instead of waiting for the next day.
	immediate, err := refuseIfLocked(ctx, tx, scheduleLoosens(config, change, now))
	if err != nil {
		return ScheduleChange{}, config, 0, err
	}
	if change.SettingsLock != nil && change.SettingsLock.Enabled {
		var pinSet bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings_pin WHERE replaced_at IS NULL)`).Scan(&pinSet); err != nil {
			return ScheduleChange{}, config, 0, err
		}
		if !pinSet {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: set the PIN in the panel before turning the settings lock on", ErrInvalidAgentChange)
		}
	}
	closed, times, addresses := config.ClosedByDefault, append([]policy.Window{}, config.DistractionTimes...), append([]string{}, config.OpenAddresses...)
	locked := config.PlanGate.FromWake
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
	lock, bedtime := config.SettingsLockAt(now), config.BedtimeAt(now)
	if change.SettingsLock != nil {
		previous.SettingsLock = &lock
		if err := change.SettingsLock.Normalize(); err != nil {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
		}
		lock = *change.SettingsLock
	}
	config = config.WithSettingsLock(lock, now)
	if change.Bedtime != nil {
		previous.Bedtime = &bedtime
		if err := change.Bedtime.Normalize(); err != nil {
			return ScheduleChange{}, config, 0, fmt.Errorf("%w: %s", ErrInvalidAgentChange, err)
		}
		bedtime = *change.Bedtime
	}
	config = config.WithBedtime(bedtime, now)
	if immediate {
		// Unlocked with the PIN: what was asked applies today.
		if change.Outside != nil {
			config.Outside, config.OutsideNext, config.OutsideNextFrom = *change.Outside, nil, ""
		}
		if change.OpeningRule != nil {
			config.OpeningRule, config.OpeningRuleNext, config.OpeningRuleNextFrom = *change.OpeningRule, nil, ""
		}
		if change.Morning != nil {
			config.Morning, config.MorningNext, config.MorningNextFrom = *change.Morning, nil, ""
		}
		if change.SettingsLock != nil {
			config.SettingsLock, config.SettingsLockNext, config.SettingsLockNextFrom = *change.SettingsLock, nil, ""
		}
		if change.Bedtime != nil {
			config.Bedtime, config.BedtimeNext, config.BedtimeNextFrom = *change.Bedtime, nil, ""
		}
	}
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
	// An opening that needs confirmations opens nothing until the owner has
	// given them all; StartsAt is then when the first may be given, and the
	// opening runs LengthMinutes from the last one.
	ConfirmationsNeeded int        `json:"confirmations_needed"`
	Confirmations       int        `json:"confirmations"`
	ConfirmSeconds      int        `json:"confirm_seconds"`
	LastConfirmedAt     *time.Time `json:"last_confirmed_at,omitempty"`
	LengthMinutes       int        `json:"length_minutes"`
	// NextConfirmAt is when the next confirmation can be given.
	NextConfirmAt *time.Time `json:"next_confirm_at,omitempty"`
}

// Confirmed reports whether every confirmation the opening needs is given.
func (o Opening) Confirmed() bool { return o.Confirmations >= o.ConfirmationsNeeded }

// confirmableAt is when the next confirmation can be given: its seconds
// after the wait for the first, after the last confirmation for the rest.
func (o Opening) confirmableAt() time.Time {
	from := o.StartsAt
	if o.LastConfirmedAt != nil {
		from = *o.LastConfirmedAt
	}
	return from.Add(time.Duration(o.ConfirmSeconds) * time.Second)
}

const openingColumns = `id, purpose, sites, apps, everything, starts_at, until, created_at, ended_at, confirmations_needed, confirmations, confirm_seconds, last_confirmed_at, length_minutes`

// ErrOpeningRefused is an opening the owner's opening rule does not allow.
var ErrOpeningRefused = errors.New("opening refused")

func scanOpening(row pgx.Row) (Opening, error) {
	var opening Opening
	err := row.Scan(&opening.ID, &opening.Purpose, &opening.Sites, &opening.Apps, &opening.Everything, &opening.StartsAt, &opening.Until, &opening.CreatedAt, &opening.EndedAt,
		&opening.ConfirmationsNeeded, &opening.Confirmations, &opening.ConfirmSeconds, &opening.LastConfirmedAt, &opening.LengthMinutes)
	if err == nil && !opening.Confirmed() {
		next := opening.confirmableAt()
		opening.NextConfirmAt = &next
	}
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
	if _, err := refuseIfLocked(ctx, tx, true); err != nil {
		return AgentChange{}, Opening{}, err
	}
	config, err := latestConfig(ctx, tx)
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	now := time.Now().UTC()
	rule := config.OpeningRuleAt(now)
	length := opening.Until.Sub(now).Round(time.Minute)
	if rule.MaxMinutes > 0 && length > time.Duration(rule.MaxMinutes)*time.Minute {
		return AgentChange{}, Opening{}, fmt.Errorf("%w: an opening lasts at most %d minutes", ErrOpeningRefused, rule.MaxMinutes)
	}
	if err := checkDayCount(ctx, tx, config, now); err != nil {
		return AgentChange{}, Opening{}, err
	}
	opening.StartsAt = now.Add(time.Duration(rule.WaitMinutes) * time.Minute)
	opening.Until = opening.StartsAt.Add(length)
	opening.ConfirmationsNeeded, opening.ConfirmSeconds, opening.LengthMinutes = rule.Confirmations, rule.ConfirmSeconds, int(length/time.Minute)
	if rule.Confirmations > 0 {
		// Left unconfirmed, it lapses an hour after its confirmations could
		// have been given.
		opening.Until = opening.StartsAt.Add(time.Duration(rule.Confirmations*rule.ConfirmSeconds)*time.Second + time.Hour)
	}
	saved, err := scanOpening(tx.QueryRow(ctx, `INSERT INTO openings (purpose, sites, apps, everything, starts_at, until, confirmations_needed, confirm_seconds, length_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+openingColumns,
		opening.Purpose, opening.Sites, opening.Apps, opening.Everything, opening.StartsAt, opening.Until.UTC(), opening.ConfirmationsNeeded, opening.ConfirmSeconds, opening.LengthMinutes))
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	logged, err := insertAgentChange(ctx, tx, AgentChangeOpening, saved, map[string]int64{"end_opening": saved.ID}, reason, nil, nil)
	if err != nil {
		return AgentChange{}, Opening{}, err
	}
	return logged, saved, tx.Commit(ctx)
}

// checkDayCount refuses an opening when the day's openings are used up.
func checkDayCount(ctx context.Context, tx pgx.Tx, config policy.Config, now time.Time) error {
	rule := config.OpeningRuleAt(now)
	if rule.PerDay == 0 {
		return nil
	}
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return err
	}
	local := now.In(location)
	used, err := openingsSince(ctx, tx, time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location))
	if err != nil {
		return err
	}
	if used >= rule.PerDay {
		return fmt.Errorf("%w: all %d openings for today are used", ErrOpeningRefused, rule.PerDay)
	}
	return nil
}

// openingsSince counts the openings made since a time that opened: fully
// confirmed and not ended before they started.
func openingsSince(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, since time.Time) (int, error) {
	var used int
	err := q.QueryRow(ctx, `SELECT count(*) FROM openings WHERE created_at >= $1 AND confirmations >= confirmations_needed AND (ended_at IS NULL OR ended_at > starts_at)`, since).Scan(&used)
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

// ConfirmOpening records one confirmation of an opening. It is refused
// before the confirmation's time, after the opening ended or lapsed, and once
// every confirmation is given. The last one starts the opening now for its
// length.
func (p *Postgres) ConfirmOpening(ctx context.Context, id int64) (Opening, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Opening{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE openings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Opening{}, err
	}
	opening, err := scanOpening(tx.QueryRow(ctx, `SELECT `+openingColumns+` FROM openings WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return Opening{}, err
	}
	if _, err := refuseIfLocked(ctx, tx, true); err != nil {
		return Opening{}, err
	}
	now := time.Now().UTC()
	switch {
	case opening.EndedAt != nil || !now.Before(opening.Until):
		return Opening{}, fmt.Errorf("%w: this opening has ended", ErrOpeningRefused)
	case opening.Confirmed():
		return Opening{}, fmt.Errorf("%w: this opening needs no more confirmations", ErrOpeningRefused)
	case now.Before(opening.confirmableAt()):
		return Opening{}, fmt.Errorf("%w: wait %d more seconds", ErrOpeningRefused, int(opening.confirmableAt().Sub(now).Seconds())+1)
	}
	starts, until := opening.StartsAt, opening.Until
	if opening.Confirmations+1 >= opening.ConfirmationsNeeded {
		config, err := latestConfig(ctx, tx)
		if err != nil {
			return Opening{}, err
		}
		if err := checkDayCount(ctx, tx, config, now); err != nil {
			return Opening{}, err
		}
		starts, until = now, now.Add(time.Duration(opening.LengthMinutes)*time.Minute)
	}
	confirmed, err := scanOpening(tx.QueryRow(ctx, `UPDATE openings SET confirmations = confirmations + 1, last_confirmed_at = $2, starts_at = $3, until = $4 WHERE id = $1 RETURNING `+openingColumns,
		id, now, starts, until))
	if err != nil {
		return Opening{}, err
	}
	return confirmed, tx.Commit(ctx)
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
		if !opening.Confirmed() {
			continue
		}
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
