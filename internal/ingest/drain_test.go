package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

func TestDrainRetriesAfterCentralFailureThenAcknowledges(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	queueStore, err := queue.Open(filepath.Join(t.TempDir(), "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	if _, err := queueStore.Enqueue(context.Background(), drainObservation(now), now); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing credentials")
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	first, err := Drain(context.Background(), queueStore, Client{BaseURL: server.URL, Token: "token"}, DrainOptions{Limit: 10, LeaseDuration: time.Minute, RetryAfter: 5 * time.Minute, Now: now, DeviceID: "mac", Version: "test"})
	server.Close()
	if err == nil || first.Claimed != 1 {
		t.Fatalf("first drain = %+v, %v", first, err)
	}
	accepted := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var batch model.Batch
		if err := json.NewDecoder(request.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		if batch.ID == "" || len(batch.Observations) != 1 {
			t.Fatalf("batch = %+v", batch)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		json.NewEncoder(writer).Encode(map[string]any{"batch_id": batch.ID, "observations_seen": 1, "revisions_created": 1})
	}))
	defer accepted.Close()
	second, err := Drain(context.Background(), queueStore, Client{BaseURL: accepted.URL, Token: "token"}, DrainOptions{Limit: 10, LeaseDuration: time.Minute, RetryAfter: time.Minute, Now: now.Add(5 * time.Minute), DeviceID: "mac", Version: "test"})
	if err != nil || second.Acknowledged != 1 {
		t.Fatalf("second drain = %+v, %v", second, err)
	}
	stats, err := queueStore.Stats(context.Background())
	if err != nil || stats.Acknowledged != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestDrainAllUsesBoundedBatchesUntilTheQueueIsEmpty(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	queueStore, err := queue.Open(filepath.Join(t.TempDir(), "queue.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer queueStore.Close()
	for index := range 3 {
		observation := drainObservation(now.Add(time.Duration(index) * time.Minute))
		observation.SourceRecordKey = fmt.Sprintf("record-%d", index)
		if _, err := queueStore.Enqueue(context.Background(), observation, now); err != nil {
			t.Fatal(err)
		}
	}
	batches := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		batches++
		var batch model.Batch
		if err := json.NewDecoder(request.Body).Decode(&batch); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		json.NewEncoder(writer).Encode(map[string]any{"batch_id": batch.ID, "observations_seen": len(batch.Observations), "revisions_created": len(batch.Observations)})
	}))
	defer server.Close()
	result, err := DrainAll(context.Background(), queueStore, Client{BaseURL: server.URL, Token: "token"}, DrainAllOptions{
		DrainOptions: DrainOptions{Limit: 2, LeaseDuration: time.Minute, RetryAfter: time.Minute, DeviceID: "mac", Version: "test"},
		MaxBatches:   10,
	})
	if err != nil || !result.QueueExhausted || result.Batches != 2 || result.Claimed != 3 || result.Acknowledged != 3 || result.RevisionsMade != 3 || batches != 2 {
		t.Fatalf("drain all = %+v, batches=%d, err=%v", result, batches, err)
	}
}

func drainObservation(now time.Time) model.Observation {
	return model.Observation{
		SourceInstanceKey: "activitywatch:mac:window",
		SourceKind:        "activitywatch",
		DeviceID:          "mac",
		SourceRecordKey:   "one",
		Kind:              "activitywatch.event",
		OccurredAt:        now.Add(-time.Minute),
		CollectedAt:       now,
		OriginalPayload:   json.RawMessage(`{"id":1}`),
	}
}
