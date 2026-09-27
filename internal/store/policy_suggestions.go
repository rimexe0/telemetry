package store

import (
	"context"
	"fmt"
	"time"
)

type AppUsageSuggestion struct {
	Package  string    `json:"package"`
	Events   int       `json:"events"`
	LastSeen time.Time `json:"last_seen"`
}

// AndroidAppUsage ranks Android packages by retained usage events so the owner
// can choose what to block. It only suggests; nothing is added automatically.
func (p *Postgres) AndroidAppUsage(ctx context.Context, since time.Time, limit int) ([]AppUsageSuggestion, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	rows, err := p.pool.Query(ctx, `
		SELECT convert_from(r.original_payload, 'UTF8')::jsonb->>'package_name' AS package, COUNT(*), MAX(o.occurred_at)
		FROM observations o JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.source_instance_key LIKE 'android:%:usage' AND o.occurred_at >= $1
		GROUP BY 1 HAVING convert_from(r.original_payload, 'UTF8')::jsonb->>'package_name' IS NOT NULL
		ORDER BY 2 DESC LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AppUsageSuggestion{}
	for rows.Next() {
		var item AppUsageSuggestion
		if err := rows.Scan(&item.Package, &item.Events, &item.LastSeen); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
