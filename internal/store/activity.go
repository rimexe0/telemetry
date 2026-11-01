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

// CodingRecords returns the current T3 projects and threads, the T3 turns
// requested since from or still unfinished, and the Codex turns since from.
func (p *Postgres) CodingRecords(ctx context.Context, from time.Time) ([]activity.Record, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT o.source_instance_key, si.device_id, o.kind, o.occurred_at, o.ended_at, r.projection
		FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		WHERE (si.source_kind = 't3' AND (o.kind IN ('t3.project', 't3.thread')
			OR (o.kind = 't3.turn' AND (o.occurred_at >= $1 OR o.ended_at IS NULL OR o.ended_at >= $1))
			OR (o.kind = 't3.open' AND o.ended_at >= $1)))
			OR (o.source_instance_key LIKE 'activitywatch:%:aw-watcher-codex%' AND o.occurred_at >= $1)
		LIMIT $2`, from, maxActivityRecord+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []activity.Record{}
	for rows.Next() {
		var record activity.Record
		if err := rows.Scan(&record.Source, &record.Device, &record.Kind, &record.Start, &record.End, &record.Payload); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if len(records) > maxActivityRecord {
		return nil, ErrActivityRangeTooLarge
	}
	return records, rows.Err()
}

// HealthRecords returns the sleep, steps and heart-rate records that began
// in [from, to), as activity.HealthDays reads them.
func (p *Postgres) HealthRecords(ctx context.Context, from, to time.Time) ([]activity.Record, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT o.source_instance_key, si.device_id, o.kind, o.occurred_at, o.ended_at, r.projection
		FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		WHERE o.kind = ANY($1) AND o.occurred_at >= $2 AND o.occurred_at < $3
		LIMIT $4`, activity.HealthKinds, from, to, maxActivityRecord+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []activity.Record{}
	for rows.Next() {
		var record activity.Record
		if err := rows.Scan(&record.Source, &record.Device, &record.Kind, &record.Start, &record.End, &record.Payload); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if len(records) > maxActivityRecord {
		return nil, ErrActivityRangeTooLarge
	}
	return records, rows.Err()
}

// PlannedBlocks gives a plan's blocks in the shape a plan review needs.
func PlannedBlocks(plan *Plan) []activity.PlannedBlock {
	blocks := []activity.PlannedBlock{}
	if plan == nil {
		return blocks
	}
	for _, block := range plan.Blocks {
		blocks = append(blocks, activity.PlannedBlock{ID: block.ID, Title: block.Title, TaskID: block.ActivityID, Start: block.StartTime,
			End: block.StartTime.Add(time.Duration(block.DurationMinutes) * time.Minute), State: block.State, Closes: block.ActivityID != nil || block.Focus})
	}
	return blocks
}
