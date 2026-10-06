package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

// LatestEmergency is the newest emergency unlock started from a device that
// is still open at or after from, or nil.
func (p *Postgres) LatestEmergency(ctx context.Context, from time.Time) (*policy.Emergency, error) {
	rows, err := p.pool.Query(ctx, `SELECT r.projection FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.kind = 'android.policy_emergency' AND o.occurred_at >= $1
		ORDER BY o.occurred_at DESC LIMIT 5`, from.Add(-2*time.Hour))
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
		if err1 != nil || err2 != nil || !until.After(started) || until.Sub(started) > 2*time.Hour || !until.After(from) {
			continue
		}
		return &policy.Emergency{StartedAt: started.UTC(), Until: until.UTC()}, nil
	}
	return nil, rows.Err()
}
