package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/budget"
	"github.com/rimexe0/telemetry/internal/policy"
)

// The distraction budget's records (owner decision 2026-11-26). The budget
// itself is worked out by package budget from these and the activity
// records; nothing here is deleted.

const AgentChangeHabits = "habits"

// BudgetStore keeps habits and their ticks, doors, grayscale events, the
// owner's reasons and the day snapshots. Postgres implements it.
type BudgetStore interface {
	ListHabits(ctx context.Context) ([]budget.Habit, error)
	ApplyHabitChanges(ctx context.Context, changes []HabitChange, reason string) (AgentChange, []budget.Habit, error)
	HabitEvents(ctx context.Context, fromDay string) ([]budget.HabitEvent, error)
	RecordHabitEvent(ctx context.Context, event budget.HabitEvent) (budget.HabitEvent, error)
	BudgetDoors(ctx context.Context, since time.Time) ([]budget.Door, error)
	CreateDoor(ctx context.Context, door budget.Door) (budget.Door, error)
	CancelDoor(ctx context.Context, id int64, at time.Time) (budget.Door, error)
	RecordDoorBack(ctx context.Context, back budget.DoorBack) (budget.DoorBack, error)
	DoorBacks(ctx context.Context, since time.Time) ([]budget.DoorBack, error)
	RecordGrayscale(ctx context.Context, event budget.GrayscaleEvent) (budget.GrayscaleEvent, error)
	GrayscaleEvents(ctx context.Context, since time.Time) ([]budget.GrayscaleEvent, error)
	Reasons(ctx context.Context) ([]string, error)
	SaveReasons(ctx context.Context, reasons []string) ([]string, error)
	BudgetDays(ctx context.Context, limit int) ([]budget.Snapshot, error)
	BudgetDay(ctx context.Context, day string) (*budget.Snapshot, error)
	SaveBudgetDay(ctx context.Context, snapshot budget.Snapshot) (bool, error)
	// ReviseBudgetDay keeps a corrected snapshot of a day that already has
	// one, as a new revision, when it says something else than the newest.
	ReviseBudgetDay(ctx context.Context, snapshot budget.Snapshot, cause string) (bool, error)
	// PhoneLastDelivered is when the server last received anything from a
	// phone, or nil when no phone ever delivered.
	PhoneLastDelivered(ctx context.Context) (*time.Time, error)
	// OpenSpans are the stretches openings and emergency unlocks had
	// distractions open in [from, to), ended ones included.
	OpenSpans(ctx context.Context, from, to time.Time) ([]budget.OpenSpan, error)
}

var _ BudgetStore = (*Postgres)(nil)

var (
	// ErrInvalidHabit is a habit change or tick that cannot be stored.
	ErrInvalidHabit = errors.New("invalid habit")
	// ErrDoorConflict is a door made while another is waiting, open or
	// in its window.
	ErrDoorConflict = errors.New("a door is already waiting or open")
	// ErrDoorNotWaiting is a cancel after the door opened.
	ErrDoorNotWaiting = errors.New("only a door still waiting can be cancelled")
)

// HabitChange adds a habit or changes one. Fields left out stay as they are.
type HabitChange struct {
	Action   string           `json:"action"`
	ID       *int64           `json:"id,omitempty"`
	Name     *string          `json:"name,omitempty"`
	Position *int             `json:"position,omitempty"`
	Weekdays *budget.Weekdays `json:"weekdays,omitempty"`
	Archived *bool            `json:"archived,omitempty"`
	// Rule sets the habit's rule; ClearRule removes it.
	Rule      *budget.HabitRule `json:"rule,omitempty"`
	ClearRule bool              `json:"clear_rule,omitempty"`
}

const habitColumns = `id, name, position, weekdays, created_at, archived_at, rule`

func scanHabit(row pgx.Row) (budget.Habit, error) {
	var habit budget.Habit
	var weekdays []int16
	var rule []byte
	err := row.Scan(&habit.ID, &habit.Name, &habit.Position, &weekdays, &habit.CreatedAt, &habit.ArchivedAt, &rule)
	if errors.Is(err, pgx.ErrNoRows) {
		return habit, ErrNotFound
	}
	habit.Weekdays = budget.Weekdays{}
	for _, day := range weekdays {
		habit.Weekdays = append(habit.Weekdays, int(day))
	}
	if err == nil && len(rule) > 0 && string(rule) != "null" {
		var parsed budget.HabitRule
		if json.Unmarshal(rule, &parsed) == nil {
			habit.Rule = &parsed
		}
	}
	return habit, err
}

// ruleColumn is a rule as the rule column keeps it: JSON, or NULL.
func ruleColumn(rule *budget.HabitRule) (any, error) {
	if rule == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

// changedRule is the rule a change leaves: checked and filled when set.
func changedRule(change HabitChange, before *budget.HabitRule, i int) (*budget.HabitRule, error) {
	switch {
	case change.Rule != nil && change.ClearRule:
		return nil, fmt.Errorf("%w: change %d: give a rule or clear_rule, not both", ErrInvalidAgentChange, i+1)
	case change.ClearRule:
		return nil, nil
	case change.Rule != nil:
		rule := *change.Rule
		if rule.Place != nil {
			place := *rule.Place
			rule.Place = &place
		}
		if err := rule.Normalize(); err != nil {
			return nil, fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
		}
		return &rule, nil
	}
	return before, nil
}

func weekdaysColumn(days budget.Weekdays) []int16 {
	result := []int16{}
	for _, day := range days {
		result = append(result, int16(day))
	}
	return result
}

func (p *Postgres) ListHabits(ctx context.Context) ([]budget.Habit, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+habitColumns+` FROM habits ORDER BY position, id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	habits := []budget.Habit{}
	for rows.Next() {
		habit, err := scanHabit(rows)
		if err != nil {
			return nil, err
		}
		habits = append(habits, habit)
	}
	return habits, rows.Err()
}

// applyHabitsTx applies changes and returns the habits written and the
// changes that undo them: a habit added is archived again, a habit changed
// gets its old fields back.
func applyHabitsTx(ctx context.Context, tx pgx.Tx, changes []HabitChange) ([]budget.Habit, []HabitChange, error) {
	if len(changes) == 0 || len(changes) > 50 {
		return nil, nil, fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	written, inverse := []budget.Habit{}, []HabitChange{}
	for i, change := range changes {
		switch change.Action {
		case "add":
			if change.Name == nil {
				return nil, nil, fmt.Errorf("%w: change %d: a new habit needs a name", ErrInvalidAgentChange, i+1)
			}
			name, err := budget.ValidateName(*change.Name)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
			}
			position := 0
			if change.Position != nil {
				position = *change.Position
			} else if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM habits WHERE archived_at IS NULL`).Scan(&position); err != nil {
				return nil, nil, err
			}
			weekdays := budget.Weekdays{}
			if change.Weekdays != nil {
				weekdays = *change.Weekdays
			}
			rule, err := changedRule(change, nil, i)
			if err != nil {
				return nil, nil, err
			}
			ruleValue, err := ruleColumn(rule)
			if err != nil {
				return nil, nil, err
			}
			habit, err := scanHabit(tx.QueryRow(ctx, `INSERT INTO habits (name, position, weekdays, rule) VALUES ($1, $2, $3, $4::jsonb) RETURNING `+habitColumns,
				name, position, weekdaysColumn(weekdays), ruleValue))
			if err != nil {
				return nil, nil, err
			}
			archived := true
			written = append(written, habit)
			inverse = append(inverse, HabitChange{Action: "update", ID: &habit.ID, Archived: &archived})
		case "update":
			if change.ID == nil {
				return nil, nil, fmt.Errorf("%w: change %d: name the habit by id", ErrInvalidAgentChange, i+1)
			}
			before, err := scanHabit(tx.QueryRow(ctx, `SELECT `+habitColumns+` FROM habits WHERE id = $1 FOR UPDATE`, *change.ID))
			if errors.Is(err, ErrNotFound) {
				return nil, nil, fmt.Errorf("%w: change %d: habit %d does not exist", ErrInvalidAgentChange, i+1, *change.ID)
			}
			if err != nil {
				return nil, nil, err
			}
			after := before
			if change.Name != nil {
				if after.Name, err = budget.ValidateName(*change.Name); err != nil {
					return nil, nil, fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
				}
			}
			if change.Position != nil {
				after.Position = *change.Position
			}
			if change.Weekdays != nil {
				after.Weekdays = *change.Weekdays
			}
			if change.Archived != nil {
				switch {
				case *change.Archived && after.ArchivedAt == nil:
					now := time.Now().UTC()
					after.ArchivedAt = &now
				case !*change.Archived:
					after.ArchivedAt = nil
				}
			}
			if after.Rule, err = changedRule(change, before.Rule, i); err != nil {
				return nil, nil, err
			}
			ruleValue, err := ruleColumn(after.Rule)
			if err != nil {
				return nil, nil, err
			}
			habit, err := scanHabit(tx.QueryRow(ctx, `UPDATE habits SET name = $2, position = $3, weekdays = $4, archived_at = $5, rule = $6::jsonb, updated_at = now()
				WHERE id = $1 RETURNING `+habitColumns, before.ID, after.Name, after.Position, weekdaysColumn(after.Weekdays), after.ArchivedAt, ruleValue))
			if err != nil {
				return nil, nil, err
			}
			name, position, weekdays, archived := before.Name, before.Position, append(budget.Weekdays{}, before.Weekdays...), before.ArchivedAt != nil
			undo := HabitChange{Action: "update", ID: &habit.ID, Name: &name, Position: &position, Weekdays: &weekdays, Archived: &archived}
			if before.Rule != nil {
				undo.Rule = before.Rule
			} else {
				undo.ClearRule = true
			}
			written = append(written, habit)
			inverse = append(inverse, undo)
		default:
			return nil, nil, fmt.Errorf("%w: change %d: action must be add or update", ErrInvalidAgentChange, i+1)
		}
	}
	return written, inverse, nil
}

// ApplyHabitChanges applies the owner's habit changes and logs them with
// what they replaced, so each can be reverted.
func (p *Postgres) ApplyHabitChanges(ctx context.Context, changes []HabitChange, reason string) (AgentChange, []budget.Habit, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, nil, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, nil, err
	}
	defer tx.Rollback(ctx)
	written, inverse, err := applyHabitsTx(ctx, tx, changes)
	if err != nil {
		return AgentChange{}, nil, err
	}
	logged, err := insertAgentChange(ctx, tx, AgentChangeHabits, changes, inverse, reason, nil, nil)
	if err != nil {
		return AgentChange{}, nil, err
	}
	return logged, written, tx.Commit(ctx)
}

func revertHabits(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (any, any, error) {
	var restore []HabitChange
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	_, inverse, err := applyHabitsTx(ctx, tx, restore)
	return restore, inverse, err
}

const habitEventColumns = `id, habit_id, to_char(day, 'YYYY-MM-DD'), action, at, received_at, source`

func scanHabitEvent(row pgx.Row) (budget.HabitEvent, error) {
	var event budget.HabitEvent
	err := row.Scan(&event.ID, &event.HabitID, &event.Day, &event.Action, &event.At, &event.ReceivedAt, &event.Source)
	return event, err
}

// HabitEvents are every tick and untick from a budget day on.
func (p *Postgres) HabitEvents(ctx context.Context, fromDay string) ([]budget.HabitEvent, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+habitEventColumns+` FROM habit_events WHERE day >= $1::date ORDER BY at, id LIMIT 50000`, fromDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []budget.HabitEvent{}
	for rows.Next() {
		event, err := scanHabitEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// RecordHabitEvent keeps one tick or untick, received now. Callers check
// that the day is open, or was when the tick was made. A habit archived
// before the tick was made is not ticked. The same event delivered again (a
// device retrying) is kept once.
func (p *Postgres) RecordHabitEvent(ctx context.Context, event budget.HabitEvent) (budget.HabitEvent, error) {
	if event.Action != budget.ActionTick && event.Action != budget.ActionUntick {
		return budget.HabitEvent{}, fmt.Errorf("%w: action must be tick or untick", ErrInvalidHabit)
	}
	received := time.Now().UTC()
	if event.At.IsZero() {
		event.At = received
	}
	var archived *time.Time
	err := p.pool.QueryRow(ctx, `SELECT archived_at FROM habits WHERE id = $1`, event.HabitID).Scan(&archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return budget.HabitEvent{}, ErrNotFound
	}
	if err != nil {
		return budget.HabitEvent{}, err
	}
	if archived != nil && !archived.After(event.At) {
		return budget.HabitEvent{}, fmt.Errorf("%w: the habit is archived", ErrInvalidHabit)
	}
	existing, err := scanHabitEvent(p.pool.QueryRow(ctx, `SELECT `+habitEventColumns+` FROM habit_events
		WHERE habit_id = $1 AND day = $2::date AND action = $3 AND at = $4 ORDER BY id LIMIT 1`, event.HabitID, event.Day, event.Action, event.At))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return budget.HabitEvent{}, err
	}
	return scanHabitEvent(p.pool.QueryRow(ctx, `INSERT INTO habit_events (habit_id, day, action, at, received_at, source) VALUES ($1, $2::date, $3, $4, $5, $6) RETURNING `+habitEventColumns,
		event.HabitID, event.Day, event.Action, event.At, received, event.Source))
}

const doorColumns = `id, requested_at, opens_at, window_end, in_distraction_time, borrowing, use_minutes, grace_minutes, cancelled_at, source`

func scanDoor(row pgx.Row) (budget.Door, error) {
	var door budget.Door
	err := row.Scan(&door.ID, &door.RequestedAt, &door.OpensAt, &door.WindowEnd, &door.InDistractionTime, &door.Borrowing, &door.UseMinutes, &door.GraceMinutes, &door.CancelledAt, &door.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return door, ErrNotFound
	}
	return door, err
}

// BudgetDoors are the doors whose window ends after since.
func (p *Postgres) BudgetDoors(ctx context.Context, since time.Time) ([]budget.Door, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+doorColumns+` FROM budget_doors WHERE window_end > $1 OR requested_at >= $1 ORDER BY id LIMIT 500`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	doors := []budget.Door{}
	for rows.Next() {
		door, err := scanDoor(rows)
		if err != nil {
			return nil, err
		}
		doors = append(doors, door)
	}
	return doors, rows.Err()
}

// CreateDoor records a door the caller found available. One door at a time:
// another still in its window refuses it, so two devices at once make one.
func (p *Postgres) CreateDoor(ctx context.Context, door budget.Door) (budget.Door, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return budget.Door{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE budget_doors IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return budget.Door{}, err
	}
	var open bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM budget_doors WHERE cancelled_at IS NULL AND window_end > $1)`, door.RequestedAt).Scan(&open); err != nil {
		return budget.Door{}, err
	}
	if open {
		return budget.Door{}, ErrDoorConflict
	}
	saved, err := scanDoor(tx.QueryRow(ctx, `INSERT INTO budget_doors (requested_at, opens_at, window_end, in_distraction_time, borrowing, use_minutes, grace_minutes, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+doorColumns,
		door.RequestedAt, door.OpensAt, door.WindowEnd, door.InDistractionTime, door.Borrowing, door.UseMinutes, door.GraceMinutes, door.Source))
	if err != nil {
		return budget.Door{}, err
	}
	return saved, tx.Commit(ctx)
}

// CancelDoor cancels a door during its wait, at no cost.
func (p *Postgres) CancelDoor(ctx context.Context, id int64, at time.Time) (budget.Door, error) {
	door, err := scanDoor(p.pool.QueryRow(ctx, `UPDATE budget_doors SET cancelled_at = $2 WHERE id = $1 AND cancelled_at IS NULL AND opens_at > $2 RETURNING `+doorColumns, id, at))
	if !errors.Is(err, ErrNotFound) {
		return door, err
	}
	existing, err := scanDoor(p.pool.QueryRow(ctx, `SELECT `+doorColumns+` FROM budget_doors WHERE id = $1`, id))
	if err != nil {
		return budget.Door{}, err
	}
	if existing.CancelledAt != nil {
		// Cancelling twice changes nothing.
		return existing, nil
	}
	return budget.Door{}, ErrDoorNotWaiting
}

func (p *Postgres) RecordDoorBack(ctx context.Context, back budget.DoorBack) (budget.DoorBack, error) {
	if back.At.IsZero() {
		back.At = time.Now().UTC()
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO door_backs (at, source) VALUES ($1, $2) RETURNING id`, back.At, back.Source).Scan(&back.ID)
	return back, err
}

func (p *Postgres) DoorBacks(ctx context.Context, since time.Time) ([]budget.DoorBack, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, at, source FROM door_backs WHERE at >= $1 ORDER BY at LIMIT 1000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	backs := []budget.DoorBack{}
	for rows.Next() {
		var back budget.DoorBack
		if err := rows.Scan(&back.ID, &back.At, &back.Source); err != nil {
			return nil, err
		}
		backs = append(backs, back)
	}
	return backs, rows.Err()
}

// ValidGrayscale checks a grayscale report from a device.
func ValidGrayscale(event budget.GrayscaleEvent) error {
	if event.Action != budget.GrayscaleDecline && event.Action != budget.GrayscaleRestore {
		return fmt.Errorf("%w: action must be decline or restore", ErrInvalidHabit)
	}
	switch event.Platform {
	case "android", "mac", "linux":
		return nil
	}
	return fmt.Errorf("%w: platform must be android, mac or linux", ErrInvalidHabit)
}

func (p *Postgres) RecordGrayscale(ctx context.Context, event budget.GrayscaleEvent) (budget.GrayscaleEvent, error) {
	if err := ValidGrayscale(event); err != nil {
		return budget.GrayscaleEvent{}, err
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO grayscale_events (at, action, platform, source) VALUES ($1, $2, $3, $4) RETURNING id`,
		event.At, event.Action, event.Platform, event.Source).Scan(&event.ID)
	return event, err
}

func (p *Postgres) GrayscaleEvents(ctx context.Context, since time.Time) ([]budget.GrayscaleEvent, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, at, action, platform, source FROM grayscale_events WHERE at >= $1 ORDER BY at, id LIMIT 5000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []budget.GrayscaleEvent{}
	for rows.Next() {
		var event budget.GrayscaleEvent
		if err := rows.Scan(&event.ID, &event.At, &event.Action, &event.Platform, &event.Source); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// CleanReasons trims the owner's reasons and refuses too many or too long.
func CleanReasons(reasons []string) ([]string, error) {
	result := []string{}
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		if len([]rune(reason)) > 500 {
			return nil, fmt.Errorf("%w: a reason is at most 500 characters", ErrInvalidHabit)
		}
		result = append(result, reason)
	}
	if len(result) > 30 {
		return nil, fmt.Errorf("%w: at most 30 reasons", ErrInvalidHabit)
	}
	return result, nil
}

func (p *Postgres) Reasons(ctx context.Context) ([]string, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT reasons FROM owner_reasons ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	reasons := []string{}
	return reasons, json.Unmarshal(raw, &reasons)
}

// SaveReasons keeps a new version of the reasons; earlier ones stay.
func (p *Postgres) SaveReasons(ctx context.Context, reasons []string) ([]string, error) {
	reasons, err := CleanReasons(reasons)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(reasons)
	if err != nil {
		return nil, err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO owner_reasons (reasons) VALUES ($1::jsonb)`, string(encoded))
	return reasons, err
}

const budgetDayColumns = `to_char(day, 'YYYY-MM-DD'), budget, spent, earned, penalty, debt, debt_passed, use_minutes, habits_done, habits_missed, settings, closed_at, revision, cause`

func scanBudgetDay(row pgx.Row) (budget.Snapshot, error) {
	var snapshot budget.Snapshot
	var settings []byte
	err := row.Scan(&snapshot.Day, &snapshot.Budget, &snapshot.Spent, &snapshot.Earned, &snapshot.Penalty, &snapshot.Debt, &snapshot.DebtPassed, &snapshot.UseMinutes,
		&snapshot.HabitsDone, &snapshot.HabitsMissed, &settings, &snapshot.ClosedAt, &snapshot.Revision, &snapshot.Cause)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, ErrNotFound
	}
	if err != nil {
		return snapshot, err
	}
	return snapshot, json.Unmarshal(settings, &snapshot.Settings)
}

// BudgetDays are the newest day snapshots, newest first, each day at its
// newest revision.
func (p *Postgres) BudgetDays(ctx context.Context, limit int) ([]budget.Snapshot, error) {
	if limit < 1 || limit > 400 {
		limit = 30
	}
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT ON (day) `+budgetDayColumns+` FROM budget_days ORDER BY day DESC, revision DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []budget.Snapshot{}
	for rows.Next() {
		day, err := scanBudgetDay(rows)
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

// BudgetDay is one day's snapshot at its newest revision, or nil before it
// was settled.
func (p *Postgres) BudgetDay(ctx context.Context, day string) (*budget.Snapshot, error) {
	snapshot, err := scanBudgetDay(p.pool.QueryRow(ctx, `SELECT `+budgetDayColumns+` FROM budget_days WHERE day = $1::date ORDER BY revision DESC LIMIT 1`, day))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// SaveBudgetDay writes a day's first snapshot once; a day already written
// keeps it, and is corrected only by ReviseBudgetDay.
func (p *Postgres) SaveBudgetDay(ctx context.Context, snapshot budget.Snapshot) (bool, error) {
	settings, err := json.Marshal(snapshot.Settings)
	if err != nil {
		return false, err
	}
	done, missed := append([]int64{}, snapshot.HabitsDone...), append([]int64{}, snapshot.HabitsMissed...)
	tag, err := p.pool.Exec(ctx, `INSERT INTO budget_days (day, budget, spent, earned, penalty, debt, debt_passed, use_minutes, habits_done, habits_missed, settings, closed_at, revision)
		VALUES ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, 1) ON CONFLICT (day, revision) DO NOTHING`,
		snapshot.Day, snapshot.Budget, snapshot.Spent, snapshot.Earned, snapshot.Penalty, snapshot.Debt, snapshot.DebtPassed, snapshot.UseMinutes,
		done, missed, string(settings), snapshot.ClosedAt)
	return err == nil && tag.RowsAffected() == 1, err
}

// ReviseBudgetDay adds a corrected snapshot of a day as its next revision,
// keeping the earlier ones. A day without a snapshot is left to the day
// close, and a correction that says what the newest revision says is not
// kept.
func (p *Postgres) ReviseBudgetDay(ctx context.Context, snapshot budget.Snapshot, cause string) (bool, error) {
	settings, err := json.Marshal(snapshot.Settings)
	if err != nil {
		return false, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// One correction of a day at a time, so two never take one revision.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('budget_days'), hashtext($1))`, snapshot.Day); err != nil {
		return false, err
	}
	newest, err := scanBudgetDay(tx.QueryRow(ctx, `SELECT `+budgetDayColumns+` FROM budget_days WHERE day = $1::date ORDER BY revision DESC LIMIT 1`, snapshot.Day))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if newest.SameDay(snapshot) {
		return false, nil
	}
	done, missed := append([]int64{}, snapshot.HabitsDone...), append([]int64{}, snapshot.HabitsMissed...)
	if _, err := tx.Exec(ctx, `INSERT INTO budget_days (day, budget, spent, earned, penalty, debt, debt_passed, use_minutes, habits_done, habits_missed, settings, closed_at, revision, cause)
		VALUES ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14)`,
		snapshot.Day, snapshot.Budget, snapshot.Spent, snapshot.Earned, snapshot.Penalty, snapshot.Debt, snapshot.DebtPassed, snapshot.UseMinutes,
		done, missed, string(settings), snapshot.ClosedAt, newest.Revision+1, cause); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// PhoneLastDelivered is when a batch last arrived from a device that has an
// android: source, or nil when none ever did.
func (p *Postgres) PhoneLastDelivered(ctx context.Context) (*time.Time, error) {
	var last *time.Time
	err := p.pool.QueryRow(ctx, `SELECT MAX(b.received_at) FROM ingestion_batches b
		WHERE b.device_id IN (SELECT device_id FROM source_instances WHERE source_instance_key LIKE 'android:%')`).Scan(&last)
	if last != nil {
		utc := last.UTC()
		last = &utc
	}
	return last, err
}

// Refused ticks; each message is said to the owner as it is.
var (
	ErrHabitDayClosed = errors.New("Bedtime has started, so today's habits are settled.")
	ErrHabitNotToday  = errors.New("This habit is not on today.")
	ErrHabitWrongDay  = errors.New("only today's budget day can be ticked")

	// A tick made earlier, on a device that could not reach the server.
	ErrHabitLateClosed   = errors.New("Bedtime had already started then, so that day's habits were settled.")
	ErrHabitTooOld       = errors.New("That day is more than a week back, so its habits are settled.")
	ErrHabitLateWrongDay = errors.New("day is not the budget day that at falls in")
	ErrHabitLateFuture   = errors.New("at is in the future")
	ErrHabitNotThen      = errors.New("This habit was not on that day.")

	// A past day set right by the owner.
	ErrHabitFixNotPast = errors.New("Only a day whose habits are settled can be set right; today's are ticked as usual.")
)

const (
	// lateTickDays is how far back a tick made on a device while the
	// server could not be reached is still taken.
	lateTickDays = 7
	// lateTickAhead is how far a device's clock may run ahead.
	lateTickAhead = 2 * time.Minute
	// settleWait is the longest a closed day waits for a silent phone
	// before it is settled.
	settleWait = 48 * time.Hour
)

// TickHabit keeps a tick or untick for today's budget day: only before
// bedtime starts, and only for a habit on today. day, when given, must be
// today's budget day.
func TickHabit(ctx context.Context, repository Repository, budgets BudgetStore, id int64, action, day, source string, now time.Time) error {
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return err
	}
	today, open := budget.TickDay(config, now)
	if day != "" && day != today {
		return fmt.Errorf("%w (%s)", ErrHabitWrongDay, today)
	}
	if !open {
		return ErrHabitDayClosed
	}
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return err
	}
	events, err := budgets.HabitEvents(ctx, today)
	if err != nil {
		return err
	}
	found, scheduled := false, false
	for _, habit := range habits {
		found = found || habit.ID == id
	}
	for _, scheduledID := range budget.Compute(budget.Inputs{Config: config, Habits: habits, Events: events}, today, now).Today.Scheduled {
		scheduled = scheduled || scheduledID == id
	}
	switch {
	case !found:
		return ErrNotFound
	case !scheduled:
		return ErrHabitNotToday
	}
	_, err = budgets.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: id, Day: today, Action: action, At: now, Source: source})
	return err
}

// FixHabitDay keeps the owner's word that a habit was done, or not, on a
// day whose habits are already settled: any day before today's budget day,
// or today's once bedtime has started. The owner asked to set right any past
// day after a night's teeth went unticked because bedtime had settled the
// day (2026-11-28). The fix is the day's newest event, so it wins over what
// the day held, and the closed days it reaches get corrected snapshots.
func FixHabitDay(ctx context.Context, repository Repository, budgets BudgetStore, id int64, action, day, source string, now time.Time) error {
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return err
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return fmt.Errorf("%w: day must be YYYY-MM-DD", ErrInvalidHabit)
	}
	today, open := budget.TickDay(config, now)
	if day > today || day == today && open {
		return ErrHabitFixNotPast
	}
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return err
	}
	var habit *budget.Habit
	for i := range habits {
		if habits[i].ID == id {
			habit = &habits[i]
		}
	}
	if habit == nil {
		return ErrNotFound
	}
	start, end := budget.DayStart(config, day), budget.DayStart(config, budget.AddDays(day, 1))
	if !habit.AppliesAt(day, start) && !habit.AppliesAt(day, end.Add(-time.Microsecond)) {
		return ErrHabitNotThen
	}
	event, err := budgets.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: id, Day: day, Action: action, At: now, Source: source})
	if err != nil {
		return err
	}
	cause := fmt.Sprintf("owner set %s of habit %d right for %s (event %d)", action, id, day, event.ID)
	_, err = ReviseBudgetDays(ctx, repository, budgets, day, cause, now)
	return err
}

// LateTick keeps a tick or untick made at at, which may have been on a
// device that could not reach the server then. It counts for the day it was
// made in, when that day's habits were still open then: from the day's
// start until bedtime started (or the day ended, with bedtime off), at most
// a week back. day, when given, must be the budget day at falls in. An
// untick undoes only ticks of its own day made before it. A day that had
// already closed gets a corrected snapshot, and so does every later day the
// correction reaches.
func LateTick(ctx context.Context, repository Repository, budgets BudgetStore, id int64, action, day string, at time.Time, source string, now time.Time) error {
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return err
	}
	at = at.UTC().Truncate(time.Microsecond)
	madeFor, open := budget.TickDay(config, at)
	switch {
	case day != "" && day != madeFor:
		return fmt.Errorf("%w (%s)", ErrHabitLateWrongDay, madeFor)
	case at.After(now.Add(lateTickAhead)):
		return ErrHabitLateFuture
	case madeFor < budget.AddDays(budget.DayOf(config, now), -lateTickDays):
		return ErrHabitTooOld
	case !open:
		return ErrHabitLateClosed
	}
	habits, err := budgets.ListHabits(ctx)
	if err != nil {
		return err
	}
	var habit *budget.Habit
	for i := range habits {
		if habits[i].ID == id {
			habit = &habits[i]
		}
	}
	if habit == nil {
		return ErrNotFound
	}
	if !habit.AppliesAt(madeFor, at) {
		return ErrHabitNotThen
	}
	event, err := budgets.RecordHabitEvent(ctx, budget.HabitEvent{HabitID: id, Day: madeFor, Action: action, At: at, Source: source})
	if err != nil {
		return err
	}
	// A delivery again after a failed correction corrects again.
	cause := fmt.Sprintf("late %s of habit %d made at %s (event %d)", action, id, at.Format(time.RFC3339), event.ID)
	_, err = ReviseBudgetDays(ctx, repository, budgets, madeFor, cause, now)
	return err
}

// budgetDaysMu keeps the day close and the corrections of closed days from
// working out the same days at once.
var budgetDaysMu sync.Mutex

// ReviseBudgetDays works out again each closed day from first on that has a
// snapshot, oldest first, and keeps a new revision of each that came out
// differently. Each starts from the newest revision of the day before, so
// penalties and debt flow forward. It stops at the first day after first
// that comes out the same, since nothing changed reaches the days after it.
func ReviseBudgetDays(ctx context.Context, repository Repository, budgets BudgetStore, first, cause string, now time.Time) (int, error) {
	budgetDaysMu.Lock()
	defer budgetDaysMu.Unlock()
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil {
		return 0, err
	}
	today := budget.DayOf(config, now)
	revised := 0
	for day := first; day < today; day = budget.AddDays(day, 1) {
		end := budget.DayEnd(config, day)
		if end.After(now) {
			break
		}
		existing, err := budgets.BudgetDay(ctx, day)
		if err != nil {
			return revised, err
		}
		if existing == nil {
			// Not settled yet, or the budget was off: the day close
			// works it out with what is there by then.
			continue
		}
		result, err := LoadBudgetDays(ctx, repository, budgets, config, day, end)
		if err != nil {
			return revised, err
		}
		ledger := result.Days[0]
		if !ledger.Enabled {
			continue
		}
		saved, err := budgets.ReviseBudgetDay(ctx, budget.SnapshotOf(ledger), cause)
		if err != nil {
			return revised, err
		}
		if saved {
			revised++
		} else if day != first {
			break
		}
	}
	return revised, nil
}

// PolicyInForce is the owner's policy with their labels merged in, as every
// device enforces it.
func PolicyInForce(ctx context.Context, repository Repository) (policy.Config, int64, error) {
	config, version := policy.Config{}, int64(0)
	stored, err := repository.LatestPolicy(ctx)
	if err != nil {
		return config, 0, err
	}
	if stored != nil {
		config, version = stored.Config, stored.ID
	}
	labels, err := repository.ListActivityLabels(ctx)
	if err != nil {
		return config, 0, err
	}
	config = policy.WithLabels(config, LabeledTargets(labels))
	return config, version, config.Normalize()
}

// LoadBudget works out the budget from the day before today through now.
func LoadBudget(ctx context.Context, repository Repository, budgets BudgetStore, config policy.Config, now time.Time) (budget.Result, error) {
	return LoadBudgetDays(ctx, repository, budgets, config, budget.AddDays(budget.DayOf(config, now), -1), now)
}

// LoadBudgetDays works out the budget days from first through the day of
// at, starting from the debt the snapshot of the day before first passed on.
func LoadBudgetDays(ctx context.Context, repository Repository, budgets BudgetStore, config policy.Config, first string, at time.Time) (budget.Result, error) {
	in, err := LoadBudgetInputs(ctx, repository, budgets, config, first, at)
	if err != nil {
		return budget.Result{}, err
	}
	return budget.Compute(in, first, at), nil
}

// LoadBudgetInputs reads the records the budget days from first through
// the day of at are worked out from.
func LoadBudgetInputs(ctx context.Context, repository Repository, budgets BudgetStore, config policy.Config, first string, at time.Time) (budget.Inputs, error) {
	in, err := loadBudgetInputs(ctx, repository, budgets, config, first, at, unsettledDays)
	if err != nil {
		return in, err
	}
	// A habit rule that opens at waking shows today's window from the wake
	// the records show.
	for _, habit := range in.Habits {
		if habit.ArchivedAt == nil && habit.Rule != nil && habit.Rule.Window.From == budget.RuleFromWake {
			in.WakeAt, err = LoadWakeTime(ctx, repository, at)
			break
		}
	}
	return in, err
}

// unsettledDays is how many days without a snapshot before first are worked
// out from the records for the debt they pass on. A day waits at most
// settleWait to be settled, so no more are ever missing.
const unsettledDays = 3

func loadBudgetInputs(ctx context.Context, repository Repository, budgets BudgetStore, config policy.Config, first string, at time.Time, unsettled int) (budget.Inputs, error) {
	in := budget.Inputs{Config: config}
	var err error
	if in.Habits, err = budgets.ListHabits(ctx); err != nil {
		return in, err
	}
	if in.Events, err = budgets.HabitEvents(ctx, budget.AddDays(first, -1)); err != nil {
		return in, err
	}
	if in.Reasons, err = budgets.Reasons(ctx); err != nil {
		return in, err
	}
	start := budget.DayStart(config, first)
	// With the budget off nothing is charged, so the activity is not read.
	if !config.BudgetAt(at).Enabled && !config.BudgetAt(at.Add(-time.Second)).Enabled && !config.BudgetAt(start).Enabled {
		return in, nil
	}
	previous := budget.AddDays(first, -1)
	before, err := budgets.BudgetDay(ctx, previous)
	if err != nil {
		return in, err
	}
	if before != nil {
		in.DebtIn = before.DebtPassed
	} else if end := budget.DayEnd(config, previous); unsettled > 0 && !end.After(at) && config.BudgetAt(end.Add(-time.Second)).Enabled &&
		config.BudgetSince != nil && end.After(*config.BudgetSince) {
		// The day before is over but waits to be settled: what it passes on
		// is worked out from the records until its snapshot is written.
		earlier, err := loadBudgetInputs(ctx, repository, budgets, config, previous, end, unsettled-1)
		if err != nil {
			return in, err
		}
		in.DebtIn = budget.SnapshotOf(budget.Compute(earlier, previous, end).Days[0]).DebtPassed
	}
	if in.Doors, err = budgets.BudgetDoors(ctx, start.Add(-2*time.Hour)); err != nil {
		return in, err
	}
	if in.Open, err = budgets.OpenSpans(ctx, start.Add(-2*time.Hour), at); err != nil {
		return in, err
	}
	// A decline the evening before still holds through that night.
	if in.Grayscale, err = budgets.GrayscaleEvents(ctx, start.Add(-24*time.Hour)); err != nil {
		return in, err
	}
	if at.After(start) {
		records, _, err := repository.ActivityRecords(ctx, start, at)
		if err != nil {
			return in, err
		}
		in.Segments = activity.Build(records, start, at).Segments
	}
	return in, nil
}

// CloseBudgetDays writes the snapshot of each ended budget day of the last
// week that has none, oldest first, so each starts from the one before.
//
// A day waits to be settled while the phone has delivered nothing since the
// day ended, for at most settleWait: ticks made on the phone while it could
// not reach the server arrive with its next delivery, and a silent phone is
// missing data, not a day without habits. A tick that arrives after the day
// was settled still corrects it with a new revision (LateTick). Without any
// phone, a day is settled when it ends.
func CloseBudgetDays(ctx context.Context, repository Repository, budgets BudgetStore, now time.Time) (int, error) {
	budgetDaysMu.Lock()
	defer budgetDaysMu.Unlock()
	config, _, err := PolicyInForce(ctx, repository)
	if err != nil || config.BudgetSince == nil {
		return 0, err
	}
	today := budget.DayOf(config, now)
	first := budget.AddDays(today, -7)
	if since := budget.DayOf(config, *config.BudgetSince); since > first {
		first = since
	}
	written := 0
	var phone *time.Time
	phoneRead := false
	for day := first; day < today; day = budget.AddDays(day, 1) {
		end := budget.DayEnd(config, day)
		if end.After(now) {
			break
		}
		existing, err := budgets.BudgetDay(ctx, day)
		if err != nil {
			return written, err
		}
		if existing != nil {
			continue
		}
		if !phoneRead {
			if phone, err = budgets.PhoneLastDelivered(ctx); err != nil {
				return written, err
			}
			phoneRead = true
		}
		if phone != nil && phone.Before(end) && now.Before(end.Add(settleWait)) {
			// The days after start from this one, so they wait too.
			break
		}
		result, err := LoadBudgetDays(ctx, repository, budgets, config, day, end)
		if err != nil {
			return written, err
		}
		ledger := result.Days[0]
		if !ledger.Enabled {
			continue
		}
		saved, err := budgets.SaveBudgetDay(ctx, budget.SnapshotOf(ledger))
		if err != nil {
			return written, err
		}
		if saved {
			written++
		}
	}
	return written, nil
}

// OpenSpans reads the openings that were confirmed and the emergency
// unlocks from older phone apps that overlap [from, to): use during them is
// charged, so ending an opening early does not make its use free.
func (p *Postgres) OpenSpans(ctx context.Context, from, to time.Time) ([]budget.OpenSpan, error) {
	spans := []budget.OpenSpan{}
	rows, err := p.pool.Query(ctx, `SELECT starts_at, until, ended_at FROM openings
		WHERE until > $1 AND starts_at < $2 AND confirmations >= confirmations_needed ORDER BY id LIMIT 500`, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var start, until time.Time
		var ended *time.Time
		if err := rows.Scan(&start, &until, &ended); err != nil {
			rows.Close()
			return nil, err
		}
		if ended != nil && ended.Before(until) {
			until = *ended
		}
		spans = append(spans, budget.OpenSpan{From: start.UTC(), To: until.UTC()})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = p.pool.Query(ctx, `SELECT r.projection FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.kind = 'android.policy_emergency' AND o.occurred_at >= $1 AND o.occurred_at < $2
		ORDER BY o.occurred_at LIMIT 500`, from.Add(-2*time.Hour), to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var payload struct {
			Started string `json:"started_at"`
			Until   string `json:"until"`
		}
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		started, err1 := time.Parse(time.RFC3339Nano, payload.Started)
		until, err2 := time.Parse(time.RFC3339Nano, payload.Until)
		// A device's own clock sets these; no unlock is longer than two hours.
		if err1 != nil || err2 != nil || !until.After(started) || until.Sub(started) > 2*time.Hour {
			continue
		}
		spans = append(spans, budget.OpenSpan{From: started.UTC(), To: until.UTC()})
	}
	return spans, rows.Err()
}
