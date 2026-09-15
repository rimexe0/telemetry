// Package store defines the central durable telemetry repository.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

type Receipt struct {
	BatchID          string `json:"batch_id"`
	ObservationsSeen int    `json:"observations_seen"`
	RevisionsCreated int    `json:"revisions_created"`
}

type Query struct {
	SourceInstanceKey string
	From              *time.Time
	To                *time.Time
	Limit             int
}

type ObservationView struct {
	SourceInstanceKey string          `json:"source_instance_key"`
	SourceKind        string          `json:"source_kind"`
	DeviceID          string          `json:"device_id"`
	SourceRecordKey   string          `json:"source_record_key"`
	Kind              string          `json:"kind"`
	OccurredAt        time.Time       `json:"occurred_at"`
	EndedAt           *time.Time      `json:"ended_at,omitempty"`
	CollectedAt       time.Time       `json:"collected_at"`
	RevisionDigest    string          `json:"revision_digest"`
	OriginalPayload   json.RawMessage `json:"original_payload"`
	Projection        json.RawMessage `json:"projection"`
}

type SourceSummary struct {
	SourceInstanceKey string     `json:"source_instance_key"`
	SourceKind        string     `json:"source_kind"`
	DeviceID          string     `json:"device_id"`
	FirstCollectedAt  time.Time  `json:"first_collected_at"`
	LastCollectedAt   *time.Time `json:"last_collected_at,omitempty"`
	ObservationCount  int64      `json:"observation_count"`
}

// CollectionHealthQuery bounds the source and resource history returned by a
// collection health read. Resource samples are returned newest first for each
// device.
type CollectionHealthQuery struct {
	SourceLimit     int
	ResourceSamples int
}

type CollectionHealthDevice struct {
	DeviceID        string            `json:"device_id"`
	LastReceivedAt  *time.Time        `json:"last_received_at,omitempty"`
	ResourceHistory []ObservationView `json:"resource_history"`
}

type CollectionHealth struct {
	CheckedAt        time.Time                `json:"checked_at"`
	Sources          []SourceSummary          `json:"sources"`
	SourcesTruncated bool                     `json:"sources_truncated"`
	Devices          []CollectionHealthDevice `json:"devices"`
}

// Repository is deliberately bounded. HTTP and MCP use this interface rather
// than receiving raw database access.
type Repository interface {
	Ingest(context.Context, model.Batch) (Receipt, error)
	QueryObservations(context.Context, Query) ([]ObservationView, error)
	ListSources(context.Context, int) ([]SourceSummary, error)
	CollectionHealth(context.Context, CollectionHealthQuery) (CollectionHealth, error)
	Ping(context.Context) error
}
