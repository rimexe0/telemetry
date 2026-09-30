package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/silence"
)

type CollectionIncident struct {
	ID         int64          `json:"id"`
	Device     string         `json:"device"`
	Kind       string         `json:"kind"`
	StartedAt  time.Time      `json:"started_at"`
	DetectedAt time.Time      `json:"detected_at"`
	EndedAt    *time.Time     `json:"ended_at,omitempty"`
	Detail     map[string]any `json:"detail"`
}

// CollectionEvidence reads what the silence checker needs.
func (p *Postgres) CollectionEvidence(ctx context.Context) (silence.Evidence, error) {
	evidence := silence.Evidence{}
	if err := p.pool.QueryRow(ctx, `SELECT MAX(received_at) FROM ingestion_batches WHERE device_id LIKE 'android-%'`).Scan(&evidence.PhoneLastReceived); err != nil {
		return evidence, err
	}
	var at time.Time
	var payload []byte
	err := p.pool.QueryRow(ctx, `
		SELECT o.occurred_at, r.projection
		FROM observations o JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE o.source_instance_key LIKE 'telemetry-desktop:%:collector' AND o.kind = 'telemetry.collector_status'
		ORDER BY o.occurred_at DESC LIMIT 1`).Scan(&at, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence, nil
	}
	if err != nil {
		return evidence, err
	}
	var status struct {
		State          string     `json:"state"`
		TitlesReadable bool       `json:"titles_readable"`
		LastWindowAt   *time.Time `json:"last_window_sample_at"`
	}
	if err := json.Unmarshal(payload, &status); err != nil {
		return evidence, err
	}
	evidence.Desktop = &silence.DesktopStatus{At: at, State: status.State, TitlesReadable: status.TitlesReadable, LastWindowAt: status.LastWindowAt}
	return evidence, nil
}

// SyncCollectionIncidents opens an incident for each new finding and closes
// open incidents that no longer hold. It returns the incidents it opened.
func (p *Postgres) SyncCollectionIncidents(ctx context.Context, findings []silence.Finding, now time.Time) ([]CollectionIncident, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	holding := map[string]bool{}
	opened := []CollectionIncident{}
	for _, finding := range findings {
		holding[finding.Device+"\x00"+finding.Kind] = true
		detail, err := json.Marshal(finding.Detail)
		if err != nil {
			return nil, err
		}
		incident := CollectionIncident{Device: finding.Device, Kind: finding.Kind, StartedAt: finding.Since, DetectedAt: now, Detail: finding.Detail}
		err = tx.QueryRow(ctx, `
			INSERT INTO collection_incidents (device, kind, started_at, detected_at, detail)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (device, kind) WHERE ended_at IS NULL DO NOTHING
			RETURNING id`, finding.Device, finding.Kind, finding.Since, now, detail).Scan(&incident.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		opened = append(opened, incident)
	}
	rows, err := tx.Query(ctx, `SELECT id, device, kind FROM collection_incidents WHERE ended_at IS NULL`)
	if err != nil {
		return nil, err
	}
	closing := []int64{}
	for rows.Next() {
		var id int64
		var device, kind string
		if err := rows.Scan(&id, &device, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		if !holding[device+"\x00"+kind] {
			closing = append(closing, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(closing) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE collection_incidents SET ended_at = $1 WHERE id = ANY($2)`, now, closing); err != nil {
			return nil, err
		}
	}
	return opened, tx.Commit(ctx)
}

// ListCollectionIncidents returns open incidents and those that ended after
// since, newest first.
func (p *Postgres) ListCollectionIncidents(ctx context.Context, since time.Time, limit int) ([]CollectionIncident, error) {
	if limit < 1 || limit > 500 {
		return nil, errors.New("limit must be between 1 and 500")
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, device, kind, started_at, detected_at, ended_at, detail
		FROM collection_incidents
		WHERE ended_at IS NULL OR ended_at >= $1
		ORDER BY (ended_at IS NULL) DESC, detected_at DESC
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CollectionIncident{}
	for rows.Next() {
		var incident CollectionIncident
		var detail []byte
		if err := rows.Scan(&incident.ID, &incident.Device, &incident.Kind, &incident.StartedAt, &incident.DetectedAt, &incident.EndedAt, &detail); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(detail, &incident.Detail); err != nil {
			return nil, err
		}
		result = append(result, incident)
	}
	return result, rows.Err()
}
