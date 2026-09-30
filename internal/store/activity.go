package store

import (
	"context"
	"errors"
	"time"

	"github.com/rimexe0/telemetry/internal/activity"
)

// activityLookback covers events that started before the requested range but
// were still running in it (a long window span, an app opened earlier).
const (
	activityLookback  = 12 * time.Hour
	maxActivityRecord = 300000
)

var ErrActivityRangeTooLarge = errors.New("activity range has too many records; request a shorter range")

// activitySourcesSQL selects the sources that describe foreground use.
const activitySourcesSQL = `
	SELECT source_instance_key FROM source_instances
	WHERE (source_instance_key LIKE 'activitywatch:%' AND (
			source_instance_key LIKE '%aw-watcher-window%' OR
			source_instance_key LIKE '%aw-watcher-afk%' OR
			source_instance_key LIKE '%aw-watcher-web%'))
		OR (source_instance_key LIKE 'android:%' AND (
			source_instance_key LIKE '%:usage' OR source_instance_key LIKE '%:state'))`

// ActivityRecords returns effective foreground-use records overlapping
// [from, to), plus the newest record time per device group at any time.
func (p *Postgres) ActivityRecords(ctx context.Context, from, to time.Time) ([]activity.Record, map[string]time.Time, error) {
	if !to.After(from) {
		return nil, nil, errors.New("activity range end must be after its start")
	}
	rows, err := p.pool.Query(ctx, `
		SELECT o.source_instance_key, si.device_id, o.kind, o.occurred_at, o.ended_at, r.projection
		FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		WHERE o.source_instance_key IN (`+activitySourcesSQL+`)
			AND o.occurred_at >= $1 AND o.occurred_at < $2
			AND COALESCE(o.ended_at, o.occurred_at + interval '1 hour') >= $3
		ORDER BY o.occurred_at, o.id
		LIMIT $4`, from.Add(-activityLookback), to, from, maxActivityRecord+1)
	if err != nil {
		return nil, nil, err
	}
	records := []activity.Record{}
	for rows.Next() {
		var record activity.Record
		if err := rows.Scan(&record.Source, &record.Device, &record.Kind, &record.Start, &record.End, &record.Payload); err != nil {
			rows.Close()
			return nil, nil, err
		}
		records = append(records, record)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(records) > maxActivityRecord {
		return nil, nil, ErrActivityRangeTooLarge
	}
	lastSeen := map[string]time.Time{}
	sources, err := p.pool.Query(ctx, `
		SELECT s.source_instance_key,
			(SELECT MAX(o.occurred_at) FROM observations o WHERE o.source_instance_key = s.source_instance_key)
		FROM (`+activitySourcesSQL+`) s`)
	if err != nil {
		return nil, nil, err
	}
	defer sources.Close()
	for sources.Next() {
		var key string
		var last *time.Time
		if err := sources.Scan(&key, &last); err != nil {
			return nil, nil, err
		}
		device := activity.DeviceOf(key)
		if last != nil && last.After(lastSeen[device]) {
			lastSeen[device] = *last
		}
	}
	return records, lastSeen, sources.Err()
}
