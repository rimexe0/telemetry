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
	// Place, when set, makes the follow-up come due on arriving at or
	// leaving a place, no earlier than DueAt. The phone checks it against
	// its own positions and reports TriggeredAt.
	Place       *FollowUpPlace `json:"place,omitempty"`
	TriggeredAt *time.Time     `json:"triggered_at,omitempty"`
}

const (
	PlaceArrive = "arrive"
	PlaceLeave  = "leave"
)

// FollowUpPlace is where a follow-up comes due: any of Points, so "at a
// pharmacy" can be every pharmacy near home.
type FollowUpPlace struct {
	// When is "arrive" or "leave".
	When string `json:"when"`
	// Name is what to call it, such as "home" or "Migros".
	Name string `json:"name"`
	// Home also counts joining the home Wi-Fi as arriving.
	Home   bool         `json:"home,omitempty"`
	Points []PlacePoint `json:"points"`
}

// PlacePoint is a circle: inside when within Radius meters.
type PlacePoint struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Radius float64 `json:"radius"`
}

// MaxPlacePoints bounds how many places one follow-up watches.
const MaxPlacePoints = 500

// Due says whether the follow-up should be in front of the owner now.
func (f FollowUp) Due(now time.Time) bool {
	return f.State == FollowUpOpen && !now.Before(f.DueAt) && (f.Place == nil || f.TriggeredAt != nil)
}

// FollowUpChange adds a follow-up or updates fields of one. Fields left out
// stay as they are.
type FollowUpChange struct {
	Action     string         `json:"action"`
	ID         *int64         `json:"id,omitempty"`
	Title      *string        `json:"title,omitempty"`
	DueAt      *time.Time     `json:"due_at,omitempty"`
	NagMinutes *int           `json:"nag_minutes,omitempty"`
	ClearNag   bool           `json:"clear_nag,omitempty"`
	State      *string        `json:"state,omitempty"`
	Place      *FollowUpPlace `json:"place,omitempty"`
	ClearPlace bool           `json:"clear_place,omitempty"`
}

// followUpBefore is a follow-up as it was before a change; FollowUp is nil
// for one the change created.
type followUpBefore struct {
	ID       int64     `json:"id"`
	FollowUp *FollowUp `json:"follow_up"`
}

const followUpColumns = `id, title, due_at, nag_minutes, state, closed_at, closed_by, created_at, updated_at, place, triggered_at`

func scanFollowUp(row pgx.Row) (FollowUp, error) {
	var item FollowUp
	var place []byte
	err := row.Scan(&item.ID, &item.Title, &item.DueAt, &item.NagMinutes, &item.State, &item.ClosedAt, &item.ClosedBy, &item.CreatedAt, &item.UpdatedAt,
		&place, &item.TriggeredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err == nil && len(place) > 0 {
		item.Place = &FollowUpPlace{}
		err = json.Unmarshal(place, item.Place)
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
	if place := item.Place; place != nil {
		place.Name = strings.TrimSpace(place.Name)
		switch {
		case place.When != PlaceArrive && place.When != PlaceLeave:
			return errors.New(`place when must be "arrive" or "leave"`)
		case place.Name == "" || len([]rune(place.Name)) > 100:
			return errors.New("place name must be 1 to 100 characters")
		case len(place.Points) == 0 || len(place.Points) > MaxPlacePoints:
			return fmt.Errorf("a place needs 1 to %d points", MaxPlacePoints)
		}
		for _, point := range place.Points {
			if point.Lat < -90 || point.Lat > 90 || point.Lon < -180 || point.Lon > 180 || point.Radius < 20 || point.Radius > 2000 {
				return errors.New("each place point needs a valid position and a radius of 20 to 2000 meters")
			}
		}
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
	var place []byte
	if item.Place != nil {
		encoded, err := json.Marshal(item.Place)
		if err != nil {
			return FollowUp{}, err
		}
		place = encoded
	}
	if create {
		return scanFollowUp(tx.QueryRow(ctx, `INSERT INTO follow_ups (title, due_at, nag_minutes, state, closed_at, closed_by, place, triggered_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+followUpColumns, item.Title, item.DueAt.UTC(), item.NagMinutes, item.State, item.ClosedAt, item.ClosedBy,
			place, item.TriggeredAt))
	}
	return scanFollowUp(tx.QueryRow(ctx, `UPDATE follow_ups SET title=$2, due_at=$3, nag_minutes=$4, state=$5, closed_at=$6, closed_by=$7, place=$8, triggered_at=$9, updated_at=now()
		WHERE id=$1 RETURNING `+followUpColumns, item.ID, item.Title, item.DueAt.UTC(), item.NagMinutes, item.State, item.ClosedAt, item.ClosedBy,
		place, item.TriggeredAt))
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
		if change.Place != nil || change.ClearPlace {
			// A new place starts waiting again.
			item.Place, item.TriggeredAt = change.Place, nil
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

// TriggerFollowUp records that a place follow-up's place was reached, as one
// of the owner's devices saw it. Only the first report counts.
func (p *Postgres) TriggerFollowUp(ctx context.Context, id int64, at time.Time) (FollowUp, error) {
	item, err := scanFollowUp(p.pool.QueryRow(ctx, `UPDATE follow_ups SET triggered_at = $2, updated_at = now()
		WHERE id = $1 AND place IS NOT NULL AND triggered_at IS NULL RETURNING `+followUpColumns, id, at.UTC()))
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
