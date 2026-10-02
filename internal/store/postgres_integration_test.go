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

func TestPostgresCollectionHealthReturnsBoundedResourceHistory(t *testing.T) {
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

	start := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	observations := make([]model.Observation, 0, 3)
	for i := range 3 {
		observations = append(observations, model.Observation{
			SourceInstanceKey: "android:health-device:collector",
			SourceKind:        "telemetry_collector",
			DeviceID:          "health-device",
			SourceRecordKey:   fmt.Sprintf("collector:%d", i),
			Kind:              "telemetry.collector_status",
			OccurredAt:        start.Add(time.Duration(i) * time.Minute),
			CollectedAt:       start.Add(time.Duration(i) * time.Minute),
			OriginalPayload:   json.RawMessage(fmt.Sprintf(`{"sample":%d}`, i)),
		})
	}
	if _, err := database.Ingest(ctx, model.Batch{
		ID:               "health-batch",
		DeviceID:         "health-device",
		CollectorVersion: "test",
		Observations:     observations,
	}); err != nil {
		t.Fatal(err)
	}
	health, err := database.CollectionHealth(ctx, CollectionHealthQuery{SourceLimit: 1, ResourceSamples: 2})
	if err != nil {
		t.Fatal(err)
	}
	if health.SourcesTruncated || len(health.Sources) != 1 {
		t.Fatalf("sources = %+v, truncated=%v", health.Sources, health.SourcesTruncated)
	}
	if len(health.Devices) != 1 || health.Devices[0].DeviceID != "health-device" {
		t.Fatalf("devices = %+v", health.Devices)
	}
	device := health.Devices[0]
	if device.LastReceivedAt == nil || len(device.ResourceHistory) != 2 {
		t.Fatalf("device health = %+v", device)
	}
	if device.ResourceHistory[0].SourceKind != "telemetry_collector" || device.ResourceHistory[0].Kind != "telemetry.collector_status" {
		t.Fatalf("resource identity = %+v", device.ResourceHistory[0])
	}
	if device.ResourceHistory[0].SourceRecordKey != "collector:2" || device.ResourceHistory[1].SourceRecordKey != "collector:1" {
		t.Fatalf("resource order = %+v", device.ResourceHistory)
	}
}

func TestCollectionHealthRejectsInvalidBounds(t *testing.T) {
	for _, query := range []CollectionHealthQuery{
		{SourceLimit: -1, ResourceSamples: 1},
		{SourceLimit: 1001, ResourceSamples: 1},
		{SourceLimit: 1, ResourceSamples: -1},
		{SourceLimit: 1, ResourceSamples: 73},
	} {
		var database Postgres
		if _, err := database.CollectionHealth(context.Background(), query); err == nil {
			t.Fatalf("query %+v was accepted", query)
		}
	}
}

func clearIntegrationData(ctx context.Context, database *Postgres) error {
	_, err := database.pool.Exec(ctx, `
		TRUNCATE ingestion_batch_observations, observation_revisions, observations,
				source_instances, ingestion_batches,
			execution_events, execution_sessions,
				outreach_log, task_evidence, task_claims, follow_ups, openings, agent_changes, policy_proposals, activity_labels, activity_label_proposals, collection_incidents, policy_versions, activity_context_rules, plan_change_proposals, plan_block_events, plan_blocks, plan_versions, activities, planning_settings RESTART IDENTITY`)
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
