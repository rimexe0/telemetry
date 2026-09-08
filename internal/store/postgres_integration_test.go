package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

// This test deliberately requires a caller-provided isolated database. It is
// skipped in normal unit runs and truncates its test database before and after.
func TestPostgresIngestPreservesEffectiveRevisionFields(t *testing.T) {
	databaseURL := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TELEMETRY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := database.ApplyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := clearIntegrationData(ctx, database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clearIntegrationData(ctx, database); err != nil {
			t.Error(err)
		}
	})
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	first := integrationObservation(now, time.Minute, 60)
	batch := model.Batch{ID: "integration-first", DeviceID: "integration-device", CollectorVersion: "test", Observations: []model.Observation{first}}
	receipt, err := database.Ingest(ctx, batch)
	if err != nil || receipt.RevisionsCreated != 1 {
		t.Fatalf("first receipt = %+v, %v", receipt, err)
	}
	receipt, err = database.Ingest(ctx, batch)
	if err != nil || receipt.RevisionsCreated != 0 {
		t.Fatalf("repeated receipt = %+v, %v", receipt, err)
	}
	second := integrationObservation(now, 3*time.Minute, 180)
	receipt, err = database.Ingest(ctx, model.Batch{ID: "integration-revision", DeviceID: "integration-device", CollectorVersion: "test", Observations: []model.Observation{second}})
	if err != nil || receipt.RevisionsCreated != 1 {
		t.Fatalf("revision receipt = %+v, %v", receipt, err)
	}
	values, err := database.QueryObservations(ctx, Query{SourceInstanceKey: first.SourceInstanceKey, Limit: 10})
	if err != nil || len(values) != 1 {
		t.Fatalf("query = %+v, %v", values, err)
	}
	if values[0].EndedAt == nil || !values[0].EndedAt.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("effective end = %v", values[0].EndedAt)
	}
	var payload struct {
		Duration int `json:"duration"`
	}
	if err := json.Unmarshal(values[0].OriginalPayload, &payload); err != nil || payload.Duration != 180 {
		t.Fatalf("effective payload = %s, %v", values[0].OriginalPayload, err)
	}
}

func clearIntegrationData(ctx context.Context, database *Postgres) error {
	_, err := database.pool.Exec(ctx, `
		TRUNCATE ingestion_batch_observations, observation_revisions, observations,
			source_instances, ingestion_batches RESTART IDENTITY`)
	return err
}

func integrationObservation(now time.Time, duration time.Duration, durationSeconds int) model.Observation {
	end := now.Add(duration)
	return model.Observation{
		SourceInstanceKey: "activitywatch:integration-device:window",
		SourceKind:        "activitywatch",
		DeviceID:          "integration-device",
		SourceRecordKey:   "id:1",
		Kind:              "activitywatch.event",
		OccurredAt:        now,
		EndedAt:           &end,
		CollectedAt:       now.Add(duration),
		OriginalPayload:   json.RawMessage(fmt.Appendf(nil, `{"id":1,"duration":%d}`, durationSeconds)),
	}
}
