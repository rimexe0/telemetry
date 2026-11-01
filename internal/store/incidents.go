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
	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT ON (o.source_instance_key) si.device_id, o.occurred_at, r.projection
		FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		WHERE o.source_instance_key LIKE 'telemetry-desktop:%:collector' AND o.kind = 'telemetry.collector_status'
		ORDER BY o.source_instance_key, o.occurred_at DESC`)
	if err != nil {
		return evidence, err
	}
	defer rows.Close()
	for rows.Next() {
		var device string
		var at time.Time
		var payload []byte
		if err := rows.Scan(&device, &at, &payload); err != nil {
			return evidence, err
		}
		status, err := desktopStatus(device, at, payload)
		if err != nil {
			return evidence, err
		}
		evidence.Desktops = append(evidence.Desktops, status)
	}
	if err := rows.Err(); err != nil {
		return evidence, err
	}
	rows.Close()
	return evidence, p.holdAnnouncedSleep(ctx, evidence.Desktops)
}

// holdAnnouncedSleep keeps a computer asleep from its "sleeping"
// announcement until it announces waking or starting. A sleeping Mac wakes
// in the dark for maintenance without telling apps, and the collector's
// minute status then would otherwise make the silence that follows look like
// a stopped collector, and the Tailscale ping during those wakes like one
// stopped while the computer is on.
func (p *Postgres) holdAnnouncedSleep(ctx context.Context, desktops []silence.DesktopStatus) error {
	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT ON (o.source_instance_key) si.device_id, r.projection->>'state'
		FROM observations o
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		WHERE o.source_instance_key LIKE 'telemetry-desktop:%:collector' AND o.kind = 'telemetry.collector_status'
			AND r.projection->>'state' IN ('sleeping', 'awake', 'started', 'stopping')
		ORDER BY o.source_instance_key, o.occurred_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	announced := map[string]string{}
	for rows.Next() {
		var device, state string
		if err := rows.Scan(&device, &state); err != nil {
			return err
		}
		announced[device] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range desktops {
		if announced[desktops[i].DeviceID] == "sleeping" && desktops[i].State == "running" {
			desktops[i].State = "sleeping"
		}
	}
	return nil
}

// desktopStatus reads a collector status record. Version 0.1 records kept
// the window fields at the top level; later ones report per module.
func desktopStatus(device string, at time.Time, payload []byte) (silence.DesktopStatus, error) {
	type window struct {
		TitlesReadable bool       `json:"titles_readable"`
		LastWindowAt   *time.Time `json:"last_window_sample_at"`
	}
	var record struct {
		State    string                     `json:"state"`
		Hostname string                     `json:"hostname"`
		Modules  map[string]json.RawMessage `json:"modules"`
		window
	}
	if err := json.Unmarshal(payload, &record); err != nil {
		return silence.DesktopStatus{}, err
	}
	status := silence.DesktopStatus{DeviceID: device, Name: record.Hostname, At: at, State: record.State, Modules: map[string]silence.ModuleStatus{}}
	if status.Name == "" {
		status.Name = device
	}
	if record.Modules == nil {
		status.Window = &silence.WindowStatus{TitlesReadable: record.TitlesReadable, LastWindowAt: record.LastWindowAt}
		return status, nil
	}
	for name, raw := range record.Modules {
		var module struct {
			Error      string     `json:"error"`
			ErrorSince *time.Time `json:"error_since"`
		}
		if err := json.Unmarshal(raw, &module); err != nil {
			return status, err
		}
		status.Modules[name] = silence.ModuleStatus{Error: module.Error, ErrorSince: module.ErrorSince}
		if name == "window" {
			var fields window
			if err := json.Unmarshal(raw, &fields); err != nil {
				return status, err
			}
			status.Window = &silence.WindowStatus{TitlesReadable: fields.TitlesReadable, LastWindowAt: fields.LastWindowAt}
		}
	}
	return status, nil
}

// SyncCollectionIncidents opens an incident for each new finding and closes
// open incidents that no longer hold. It returns the incidents it opened.
func (p *Postgres) SyncCollectionIncidents(ctx context.Context, findings []silence.Finding, now time.Time) ([]CollectionIncident, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Read open incidents first: inserting a held finding again would be
	// rejected but still consume an id.
	rows, err := tx.Query(ctx, `SELECT id, device, kind FROM collection_incidents WHERE ended_at IS NULL`)
	if err != nil {
		return nil, err
	}
	open := map[string]int64{}
	for rows.Next() {
		var id int64
		var device, kind string
		if err := rows.Scan(&id, &device, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		open[device+"\x00"+kind] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	holding := map[string]bool{}
	opened := []CollectionIncident{}
	for _, finding := range findings {
		key := finding.Device + "\x00" + finding.Kind
		holding[key] = true
		if _, ok := open[key]; ok {
			continue
		}
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
	closing := []int64{}
	for key, id := range open {
		if !holding[key] {
			closing = append(closing, id)
		}
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
