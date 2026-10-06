package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// CalendarEvent is one occurrence from the owner's calendars, read on the
// phone, read-only.
type CalendarEvent struct {
	Title    string    `json:"title"`
	Location string    `json:"location,omitempty"`
	Calendar string    `json:"calendar"`
	AllDay   bool      `json:"all_day"`
	Busy     bool      `json:"busy"`
	Status   string    `json:"status"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// CalendarEvents are the occurrences overlapping [from, to), earliest first,
// without deleted or cancelled ones.
func (p *Postgres) CalendarEvents(ctx context.Context, from, to time.Time) ([]CalendarEvent, error) {
	rows, err := p.pool.Query(ctx, `SELECT r.projection FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.source_instance_key LIKE 'android:%:calendar' AND o.occurred_at < $2 AND coalesce(o.ended_at, o.occurred_at) > $1`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []CalendarEvent{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event CalendarEvent
		if json.Unmarshal(raw, &event) != nil || event.Status == "deleted" || event.Status == "cancelled" || event.Start.IsZero() {
			continue
		}
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	return events, rows.Err()
}
