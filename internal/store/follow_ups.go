package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const AgentChangeFollowUps = "follow_ups"

const (
	FollowUpOpen    = "open"
	FollowUpDone    = "done"
	FollowUpDropped = "dropped"
)

// FollowUp is something the owner must come back to at a time. The system
// holds it and keeps showing it once due, until it is done or dropped.
type FollowUp struct {
	ID    int64     `json:"id"`
	Title string    `json:"title"`
	DueAt time.Time `json:"due_at"`
	// NagMinutes, when set, is how often a due follow-up alerts again.
	NagMinutes *int       `json:"nag_minutes,omitempty"`
	State      string     `json:"state"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	// ClosedBy is "assistant" or "device:<id>".
	ClosedBy  string    `json:"closed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FollowUpChange adds a follow-up or updates fields of one. Fields left out
// stay as they are.
type FollowUpChange struct {
	Action     string     `json:"action"`
	ID         *int64     `json:"id,omitempty"`
	Title      *string    `json:"title,omitempty"`
	DueAt      *time.Time `json:"due_at,omitempty"`
	NagMinutes *int       `json:"nag_minutes,omitempty"`
	ClearNag   bool       `json:"clear_nag,omitempty"`
	State      *string    `json:"state,omitempty"`
}

// followUpBefore is a follow-up as it was before a change; FollowUp is nil
// for one the change created.
type followUpBefore struct {
	ID       int64     `json:"id"`
	FollowUp *FollowUp `json:"follow_up"`
}

const followUpColumns = `id, title, due_at, nag_minutes, state, closed_at, closed_by, created_at, updated_at`

func scanFollowUp(row pgx.Row) (FollowUp, error) {
	var item FollowUp
	err := row.Scan(&item.ID, &item.Title, &item.DueAt, &item.NagMinutes, &item.State, &item.ClosedAt, &item.ClosedBy, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

func validateFollowUp(item *FollowUp) error {
	item.Title = strings.TrimSpace(item.Title)
	switch {
	case item.Title == "" || len([]rune(item.Title)) > 200:
		return errors.New("title must be 1 to 200 characters")
	case item.DueAt.IsZero():
		return errors.New("a due time is required")
	case item.NagMinutes != nil && (*item.NagMinutes < 1 || *item.NagMinutes > 1440):
		return errors.New("nag_minutes must be between 1 and 1440")
	case item.State != FollowUpOpen && item.State != FollowUpDone && item.State != FollowUpDropped:
		return errors.New(`state must be "open", "done" or "dropped"`)
	}
	return nil
}

// writeFollowUpTx stores item as given; closing fields follow its state.
func writeFollowUpTx(ctx context.Context, tx pgx.Tx, item FollowUp, create bool, by string) (FollowUp, error) {
	if err := validateFollowUp(&item); err != nil {
		return FollowUp{}, fmt.Errorf("%w: %v", ErrInvalidAgentChange, err)
	}
	if item.State == FollowUpOpen {
		item.ClosedAt, item.ClosedBy = nil, ""
	} else if item.ClosedAt == nil {
		now := time.Now().UTC()
		item.ClosedAt, item.ClosedBy = &now, by
	}
	if create {
		return scanFollowUp(tx.QueryRow(ctx, `INSERT INTO follow_ups (title, due_at, nag_minutes, state, closed_at, closed_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+followUpColumns, item.Title, item.DueAt.UTC(), item.NagMinutes, item.State, item.ClosedAt, item.ClosedBy))
	}
	return scanFollowUp(tx.QueryRow(ctx, `UPDATE follow_ups SET title=$2, due_at=$3, nag_minutes=$4, state=$5, closed_at=$6, closed_by=$7, updated_at=now()
		WHERE id=$1 RETURNING `+followUpColumns, item.ID, item.Title, item.DueAt.UTC(), item.NagMinutes, item.State, item.ClosedAt, item.ClosedBy))
}

// ApplyFollowUpChanges adds or updates follow-ups at once and logs the change.
func (p *Postgres) ApplyFollowUpChanges(ctx context.Context, changes []FollowUpChange, reason string) (AgentChange, []FollowUp, error) {
	reason, err := validReason(reason)
	if err != nil {
		return AgentChange{}, nil, err
	}
	if len(changes) == 0 || len(changes) > 50 {
		return AgentChange{}, nil, fmt.Errorf("%w: give 1 to 50 changes", ErrInvalidAgentChange)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return AgentChange{}, nil, err
	}
	defer tx.Rollback(ctx)
	previous := []followUpBefore{}
	written := []FollowUp{}
	for i, change := range changes {
		var item FollowUp
		create := false
		switch change.Action {
		case "add":
			if change.ID != nil {
				return AgentChange{}, nil, fmt.Errorf("%w: change %d adds a follow-up, so it takes no id", ErrInvalidAgentChange, i+1)
			}
			item, create = FollowUp{State: FollowUpOpen}, true
		case "update":
			if change.ID == nil {
				return AgentChange{}, nil, fmt.Errorf("%w: change %d updates a follow-up, so it needs its id", ErrInvalidAgentChange, i+1)
			}
			item, err = scanFollowUp(tx.QueryRow(ctx, `SELECT `+followUpColumns+` FROM follow_ups WHERE id = $1 FOR UPDATE`, *change.ID))
			if err != nil {
				return AgentChange{}, nil, fmt.Errorf("change %d: follow-up %d: %w", i+1, *change.ID, err)
			}
			before := item
			previous = append(previous, followUpBefore{ID: item.ID, FollowUp: &before})
		default:
			return AgentChange{}, nil, fmt.Errorf(`%w: change %d action must be "add" or "update"`, ErrInvalidAgentChange, i+1)
		}
		if change.Title != nil {
			item.Title = *change.Title
		}
		if change.DueAt != nil {
			item.DueAt = *change.DueAt
		}
		if change.NagMinutes != nil {
			item.NagMinutes = change.NagMinutes
		}
		if change.ClearNag {
			item.NagMinutes = nil
		}
		if change.State != nil && *change.State != item.State {
			item.State, item.ClosedAt = *change.State, nil
		}
		saved, err := writeFollowUpTx(ctx, tx, item, create, "assistant")
		if err != nil {
			return AgentChange{}, nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		if create {
			previous = append(previous, followUpBefore{ID: saved.ID})
		}
		written = append(written, saved)
	}
	change, err := insertAgentChange(ctx, tx, AgentChangeFollowUps, written, previous, reason, nil, nil)
	if err != nil {
		return AgentChange{}, nil, err
	}
	return change, written, tx.Commit(ctx)
}

// ListFollowUps returns every open follow-up and those closed since
// closedSince, soonest due first.
func (p *Postgres) ListFollowUps(ctx context.Context, closedSince time.Time) ([]FollowUp, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+followUpColumns+` FROM follow_ups
		WHERE state = 'open' OR closed_at >= $1 ORDER BY due_at, id LIMIT 500`, closedSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []FollowUp{}
	for rows.Next() {
		item, err := scanFollowUp(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CloseFollowUp marks an open follow-up done from one of the owner's
// devices. Closing one that is already closed changes nothing.
func (p *Postgres) CloseFollowUp(ctx context.Context, id int64, by string) (FollowUp, error) {
	item, err := scanFollowUp(p.pool.QueryRow(ctx, `UPDATE follow_ups SET state = 'done', closed_at = now(), closed_by = $2, updated_at = now()
		WHERE id = $1 AND state = 'open' RETURNING `+followUpColumns, id, by))
	if errors.Is(err, ErrNotFound) {
		return scanFollowUp(p.pool.QueryRow(ctx, `SELECT `+followUpColumns+` FROM follow_ups WHERE id = $1`, id))
	}
	return item, err
}

// revertFollowUps restores each follow-up to its state before the change. One
// the change created is dropped: follow-ups are never deleted.
func revertFollowUps(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (any, any, error) {
	var restore []followUpBefore
	if err := json.Unmarshal(raw, &restore); err != nil {
		return nil, nil, err
	}
	previous := []followUpBefore{}
	written := []FollowUp{}
	for _, item := range restore {
		current, err := scanFollowUp(tx.QueryRow(ctx, `SELECT `+followUpColumns+` FROM follow_ups WHERE id = $1 FOR UPDATE`, item.ID))
		if err != nil {
			return nil, nil, fmt.Errorf("follow-up %d: %w", item.ID, err)
		}
		target := current
		if item.FollowUp != nil {
			target = *item.FollowUp
		} else {
			target.State, target.ClosedAt = FollowUpDropped, nil
		}
		saved, err := writeFollowUpTx(ctx, tx, target, false, "assistant")
		if err != nil {
			return nil, nil, err
		}
		previous = append(previous, followUpBefore{ID: current.ID, FollowUp: &current})
		written = append(written, saved)
	}
	return written, previous, nil
}
