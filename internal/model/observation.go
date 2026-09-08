// Package model contains the durable wire and storage model shared by
// collectors and the core service.
package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Observation is one source record revision. OriginalPayload is retained as
// received; Projection is a canonical, searchable representation of it.
type Observation struct {
	SourceInstanceKey   string          `json:"source_instance_key"`
	SourceKind          string          `json:"source_kind"`
	DeviceID            string          `json:"device_id"`
	SourceRecordKey     string          `json:"source_record_key"`
	Kind                string          `json:"kind"`
	OccurredAt          time.Time       `json:"occurred_at"`
	EndedAt             *time.Time      `json:"ended_at,omitempty"`
	CollectedAt         time.Time       `json:"collected_at"`
	Timezone            string          `json:"timezone,omitempty"`
	SourceSchemaVersion string          `json:"source_schema_version,omitempty"`
	OriginalPayload     json.RawMessage `json:"original_payload"`
	Projection          json.RawMessage `json:"projection"`
	RevisionDigest      string          `json:"revision_digest"`
}

// Normalize validates an observation and derives the searchable projection and
// revision digest without changing the original representation.
func (o *Observation) Normalize() error {
	for _, required := range []struct {
		field string
		value string
	}{
		{"source_instance_key", o.SourceInstanceKey},
		{"source_kind", o.SourceKind},
		{"device_id", o.DeviceID},
		{"source_record_key", o.SourceRecordKey},
		{"kind", o.Kind},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("%s is required", required.field)
		}
	}
	if o.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	if o.CollectedAt.IsZero() {
		return errors.New("collected_at is required")
	}
	if o.EndedAt != nil && o.EndedAt.Before(o.OccurredAt) {
		return errors.New("ended_at cannot precede occurred_at")
	}
	canonical, err := CanonicalJSON(o.OriginalPayload)
	if err != nil {
		return fmt.Errorf("original_payload: %w", err)
	}
	o.Projection = canonical
	digest := sha256.Sum256(canonical)
	o.RevisionDigest = hex.EncodeToString(digest[:])
	o.OccurredAt = o.OccurredAt.UTC()
	o.CollectedAt = o.CollectedAt.UTC()
	if o.EndedAt != nil {
		end := o.EndedAt.UTC()
		o.EndedAt = &end
	}
	return nil
}

// CanonicalJSON validates one JSON value and returns a deterministic encoding
// suitable for revision comparison. The received bytes remain in OriginalPayload.
func CanonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("contains more than one JSON value")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("contains more than one JSON value")
		}
		return nil, err
	}
	return json.Marshal(value)
}

// QueueItemID is stable across transport attempts and collector restarts.
func QueueItemID(o Observation) string {
	input := o.SourceInstanceKey + "\x00" + o.SourceRecordKey + "\x00" + o.RevisionDigest
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}
