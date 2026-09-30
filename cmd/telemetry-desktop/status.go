package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/ingest"
	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

// Collector states. The server treats silence after "sleeping" or "stopping"
// as announced; silence after any other state is a collection problem.
const (
	stateStarted  = "started"
	stateRunning  = "running"
	stateSleeping = "sleeping"
	stateAwake    = "awake"
	stateStopping = "stopping"
)

type statusReporter struct {
	queue    *queue.Queue
	client   *ingest.Client
	deviceID string
	health   *desktop.Health
	started  time.Time
}

// report queues one status record and, when urgent, delivers it at once:
// a sleep or stop announcement must leave before the network goes away.
func (r statusReporter) report(ctx context.Context, state string, urgent bool) error {
	now := time.Now().UTC()
	pending := int64(0)
	if stats, err := r.queue.Stats(ctx); err == nil {
		pending = stats.Pending + stats.Leased
	}
	payload, err := json.Marshal(struct {
		State     string    `json:"state"`
		Version   string    `json:"version"`
		StartedAt time.Time `json:"started_at"`
		Pending   int64     `json:"queue_pending"`
		desktop.HealthSnapshot
	}{state, collectorVersion, r.started, pending, r.health.Snapshot()})
	if err != nil {
		return err
	}
	observation := model.Observation{
		SourceInstanceKey:   "telemetry-desktop:" + r.deviceID + ":collector",
		SourceKind:          "telemetry_collector",
		DeviceID:            r.deviceID,
		SourceRecordKey:     "status:" + now.Format(time.RFC3339Nano),
		Kind:                "telemetry.collector_status",
		OccurredAt:          now,
		CollectedAt:         now,
		SourceSchemaVersion: "telemetry-desktop-status-1",
		OriginalPayload:     payload,
	}
	if err := observation.Normalize(); err != nil {
		return err
	}
	if _, err := r.queue.Enqueue(ctx, observation, now); err != nil {
		return err
	}
	if urgent && r.client != nil {
		deliverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if problem := deliver(deliverCtx, r.queue, *r.client, r.deviceID); problem != "" {
			return &deliveryError{problem}
		}
	}
	return nil
}

type deliveryError struct{ message string }

func (e *deliveryError) Error() string { return e.message }
