package store

import (
	"context"
	"time"
)

// Outreach is a message the assistant sent the owner without being asked,
// or, with an empty Message, a decision to stay quiet. Facts is what the
// message was written from.
type Outreach struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	Facts     string    `json:"facts,omitempty"`
	Delivered bool      `json:"delivered"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (p *Postgres) AddOutreach(ctx context.Context, outreach Outreach) (Outreach, error) {
	err := p.pool.QueryRow(ctx, `INSERT INTO outreach_log (kind, message, facts, delivered, error) VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`, outreach.Kind, outreach.Message, outreach.Facts, outreach.Delivered, outreach.Error).Scan(&outreach.ID, &outreach.CreatedAt)
	return outreach, err
}

// ListOutreach returns entries since a time, newest first.
func (p *Postgres) ListOutreach(ctx context.Context, since time.Time) ([]Outreach, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, kind, message, facts, delivered, error, created_at FROM outreach_log
		WHERE created_at >= $1 ORDER BY created_at DESC, id DESC LIMIT 200`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Outreach{}
	for rows.Next() {
		var outreach Outreach
		if err := rows.Scan(&outreach.ID, &outreach.Kind, &outreach.Message, &outreach.Facts, &outreach.Delivered, &outreach.Error, &outreach.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, outreach)
	}
	return result, rows.Err()
}
