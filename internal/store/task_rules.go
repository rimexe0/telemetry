package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rimexe0/telemetry/internal/activity"
)

const AgentChangeRules = "rules"

// TaskRuleChange adds a rule to a task or removes one. A removal names the
// rule by id, or by task, signal and pattern.
type TaskRuleChange struct {
	Action  string `json:"action"`
	ID      *int64 `json:"id,omitempty"`
	TaskID  int64  `json:"task_id,omitempty"`
	Signal  string `json:"signal,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	Note    string `json:"note,omitempty"`
}

const contextRuleColumns = `id, activity_id, signal, pattern, note, created_at, disabled_at`

// applyTaskRulesTx applies changes and returns the rules it touched and the
// changes that undo them. A revert is lenient: a rule that is already there,
// or already gone, is left as it is.
func applyTaskRulesTx(ctx context.Context, tx pgx.Tx, changes []TaskRuleChange, lenient bool) ([]ActivityContextRule, []TaskRuleChange, error) {
	written, inverse := []ActivityContextRule{}, []TaskRuleChange{}
	for i, change := range changes {
		switch change.Action {
		case "add":
			rule := ActivityContextRule{ActivityID: change.TaskID, Signal: change.Signal, Pattern: change.Pattern, Note: change.Note}
			if err := normaliseContextRule(&rule); err != nil {
				return nil, nil, fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
			}
			// A savepoint keeps the transaction usable after a refused insert.
			nested, err := tx.Begin(ctx)
			if err != nil {
				return nil, nil, err
			}
			created, err := scanContextRule(nested.QueryRow(ctx, `INSERT INTO activity_context_rules (activity_id, signal, pattern, note)
				VALUES ($1, $2, $3, $4) RETURNING `+contextRuleColumns, rule.ActivityID, rule.Signal, rule.Pattern, rule.Note))
			var postgresErr *pgconn.PgError
			if errors.As(err, &postgresErr) {
				_ = nested.Rollback(ctx)
				switch postgresErr.Code {
				case "23503":
					return nil, nil, fmt.Errorf("%w: change %d: task %d does not exist", ErrInvalidAgentChange, i+1, change.TaskID)
				case "23505":
					if lenient {
						continue
					}
					return nil, nil, fmt.Errorf("%w: change %d: task %d already has that rule", ErrInvalidAgentChange, i+1, change.TaskID)
				}
			}
			if err != nil {
				_ = nested.Rollback(ctx)
				return nil, nil, err
			}
			if err := nested.Commit(ctx); err != nil {
				return nil, nil, err
			}
			written = append(written, created)
			inverse = append(inverse, TaskRuleChange{Action: "remove", ID: &created.ID})
		case "remove":
			var removed ActivityContextRule
			var err error
			if change.ID != nil {
				removed, err = scanContextRule(tx.QueryRow(ctx, `UPDATE activity_context_rules SET disabled_at = now()
					WHERE id = $1 AND disabled_at IS NULL RETURNING `+contextRuleColumns, *change.ID))
			} else {
				rule := ActivityContextRule{ActivityID: change.TaskID, Signal: change.Signal, Pattern: change.Pattern}
				if err := normaliseContextRule(&rule); err != nil {
					return nil, nil, fmt.Errorf("%w: change %d: %v", ErrInvalidAgentChange, i+1, err)
				}
				removed, err = scanContextRule(tx.QueryRow(ctx, `UPDATE activity_context_rules SET disabled_at = now()
					WHERE activity_id = $1 AND signal = $2 AND lower(pattern) = lower($3) AND disabled_at IS NULL RETURNING `+contextRuleColumns,
					rule.ActivityID, rule.Signal, rule.Pattern))
			}
			if errors.Is(err, pgx.ErrNoRows) {
				if lenient {
					continue
				}
				return nil, nil, fmt.Errorf("%w: change %d: no such enabled rule", ErrInvalidAgentChange, i+1)
			}
			if err != nil {
				return nil, nil, err
			}
			written = append(written, removed)
			inverse = append(inverse, TaskRuleChange{Action: "add", TaskID: removed.ActivityID, Signal: removed.Signal, Pattern: removed.Pattern, Note: removed.Note})
		default:
			return nil, nil, fmt.Errorf(`%w: change %d action must be "add" or "remove"`, ErrInvalidAgentChange, i+1)
		}
	}
	return written, inverse, nil
}

// ApplyTaskRuleChanges changes what counts as doing a task and logs it.
func (p *Postgres) ApplyTaskRuleChanges(ctx context.Context, changes []TaskRuleChange, reason string) (AgentChange, []ActivityContextRule, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, nil, err
	}
	if len(changes) == 0 || len(changes) > 50 {
		return AgentChange{}, nil, fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	for i := range changes {
		changes[i].Action = strings.TrimSpace(changes[i].Action)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, nil, err
	}
	defer tx.Rollback(ctx)
	written, inverse, err := applyTaskRulesTx(ctx, tx, changes, false)
	if err != nil {
		return AgentChange{}, nil, err
	}
	change, err := insertAgentChange(ctx, tx, AgentChangeRules, changes, inverse, reason, nil, nil)
	if err != nil {
		return AgentChange{}, nil, err
	}
	return change, written, tx.Commit(ctx)
}

func revertTaskRules(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (any, any, error) {
	var restore []TaskRuleChange
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	_, inverse, err := applyTaskRulesTx(ctx, tx, restore, true)
	return restore, inverse, err
}

// TaskRules gives the enabled rules per task in the shape the day view needs.
func TaskRules(rules []ActivityContextRule) map[int64][]activity.TaskRule {
	byTask := map[int64][]activity.TaskRule{}
	for _, rule := range rules {
		if rule.DisabledAt == nil {
			byTask[rule.ActivityID] = append(byTask[rule.ActivityID], activity.TaskRule{Signal: rule.Signal, Pattern: rule.Pattern})
		}
	}
	return byTask
}
