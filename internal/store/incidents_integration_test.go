package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/silence"
)

func openCleanIntegration(t *testing.T) (context.Context, *Postgres) {
	t.Helper()
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
	return ctx, database
}

func TestPostgresIncidentsOpenOnceAndClose(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	finding := silence.Finding{Device: "mac", Kind: "silent", Since: now.Add(-11 * time.Minute), Detail: map[string]any{"message": "quiet"}}
	opened, err := database.SyncCollectionIncidents(ctx, []silence.Finding{finding}, now)
	if err != nil || len(opened) != 1 {
		t.Fatalf("opened = %+v, %v", opened, err)
	}
	again, err := database.SyncCollectionIncidents(ctx, []silence.Finding{finding}, now.Add(time.Minute))
	if err != nil || len(again) != 0 {
		t.Fatalf("a held finding must not open a second incident: %+v, %v", again, err)
	}
	if _, err := database.SyncCollectionIncidents(ctx, nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	incidents, err := database.ListCollectionIncidents(ctx, now, 10)
	if err != nil || len(incidents) != 1 || incidents[0].EndedAt == nil || incidents[0].Detail["message"] != "quiet" {
		t.Fatalf("incidents = %+v, %v", incidents, err)
	}
	// A finding held for many checks must not use up ids.
	for minute := 3; minute < 10; minute++ {
		if _, err := database.SyncCollectionIncidents(ctx, []silence.Finding{finding}, now.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.SyncCollectionIncidents(ctx, nil, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	next, err := database.SyncCollectionIncidents(ctx, []silence.Finding{finding}, now.Add(11*time.Minute))
	if err != nil || len(next) != 1 || next[0].ID != 3 {
		t.Fatalf("next incident = %+v, %v (ids must not jump)", next, err)
	}
}

func TestPostgresCollectionEvidenceAndActivityRecords(t *testing.T) {
	ctx, database := openCleanIntegration(t)
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	end := at.Add(time.Minute)
	observations := []model.Observation{
		{SourceInstanceKey: "telemetry-desktop:mac-1:collector", SourceKind: "telemetry_collector", DeviceID: "mac-1", SourceRecordKey: "status:1",
			Kind: "telemetry.collector_status", OccurredAt: at, CollectedAt: at,
			OriginalPayload: json.RawMessage(`{"state":"sleeping","titles_readable":true}`)},
		{SourceInstanceKey: "activitywatch:mac-1:aw-watcher-window_h", SourceKind: "activitywatch", DeviceID: "mac-1", SourceRecordKey: "td:1",
			Kind: "activitywatch.event", OccurredAt: at, EndedAt: &end, CollectedAt: at,
			OriginalPayload: json.RawMessage(`{"timestamp":"2026-09-30T10:00:00Z","duration":60,"data":{"app":"Helium","title":"x"}}`)},
	}
	if _, err := database.Ingest(ctx, model.Batch{ID: "b1", DeviceID: "mac-1", CollectorVersion: "test", Observations: observations}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Ingest(ctx, model.Batch{ID: "b2", DeviceID: "android-p", CollectorVersion: "test", Observations: []model.Observation{
		{SourceInstanceKey: "android:android-p:state", SourceKind: "android_device", DeviceID: "android-p", SourceRecordKey: "s1",
			Kind: "android.device_state", OccurredAt: at, CollectedAt: at, OriginalPayload: json.RawMessage(`{"screen_interactive":true}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	evidence, err := database.CollectionEvidence(ctx)
	if err != nil || len(evidence.Desktops) != 1 || evidence.Desktops[0].State != "sleeping" || evidence.Desktops[0].Window == nil || evidence.PhoneLastReceived == nil {
		t.Fatalf("evidence = %+v, %v", evidence, err)
	}
	later := at.Add(time.Minute)
	if _, err := database.Ingest(ctx, model.Batch{ID: "b3", DeviceID: "datalix", CollectorVersion: "test", Observations: []model.Observation{
		{SourceInstanceKey: "telemetry-desktop:datalix:collector", SourceKind: "telemetry_collector", DeviceID: "datalix", SourceRecordKey: "status:1",
			Kind: "telemetry.collector_status", OccurredAt: later, CollectedAt: later,
			OriginalPayload: json.RawMessage(`{"state":"running","hostname":"vps1","modules":{"t3":{"error":"schema changed","error_since":"2026-09-30T09:00:00Z"}}}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	evidence, err = database.CollectionEvidence(ctx)
	if err != nil || len(evidence.Desktops) != 2 {
		t.Fatalf("evidence = %+v, %v", evidence, err)
	}
	for _, status := range evidence.Desktops {
		if status.DeviceID == "datalix" && (status.Name != "vps1" || status.Window != nil || status.Modules["t3"].ErrorSince == nil) {
			t.Fatalf("datalix status = %+v", status)
		}
	}
	records, lastSeen, err := database.ActivityRecords(ctx, at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil || len(records) != 2 || lastSeen["mac"].IsZero() || lastSeen["phone"].IsZero() {
		t.Fatalf("records = %d, last = %v, %v", len(records), lastSeen, err)
	}
}
