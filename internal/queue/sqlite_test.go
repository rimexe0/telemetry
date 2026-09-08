package queue

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
)

func TestQueueDeduplicatesAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.sqlite")
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	q := openTestQueue(t, path)
	inserted, err := q.Enqueue(context.Background(), testObservation(now), now)
	if err != nil || !inserted {
		t.Fatalf("first enqueue = %v, %v", inserted, err)
	}
	inserted, err = q.Enqueue(context.Background(), testObservation(now), now)
	if err != nil || inserted {
		t.Fatalf("repeat enqueue = %v, %v", inserted, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = openTestQueue(t, path)
	defer q.Close()
	items, err := q.Claim(context.Background(), 1, time.Minute, now)
	if err != nil || len(items) != 1 {
		t.Fatalf("claim = %d items, %v", len(items), err)
	}
	if err := q.Acknowledge(context.Background(), items[0], now); err != nil {
		t.Fatal(err)
	}
	stats, err := q.Stats(context.Background())
	if err != nil || stats.Acknowledged != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestQueueLeaseFencesStaleAcknowledgement(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	q := openTestQueue(t, filepath.Join(t.TempDir(), "collector.sqlite"))
	defer q.Close()
	if _, err := q.Enqueue(context.Background(), testObservation(now), now); err != nil {
		t.Fatal(err)
	}
	first, err := q.Claim(context.Background(), 1, time.Minute, now)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %d items, %v", len(first), err)
	}
	second, err := q.Claim(context.Background(), 1, time.Minute, now.Add(2*time.Minute))
	if err != nil || len(second) != 1 {
		t.Fatalf("second claim = %d items, %v", len(second), err)
	}
	if err := q.Acknowledge(context.Background(), first[0], now.Add(2*time.Minute)); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("stale acknowledgement error = %v", err)
	}
	if err := q.Acknowledge(context.Background(), second[0], now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestQueueReleaseDefersRetry(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	q := openTestQueue(t, filepath.Join(t.TempDir(), "collector.sqlite"))
	defer q.Close()
	if _, err := q.Enqueue(context.Background(), testObservation(now), now); err != nil {
		t.Fatal(err)
	}
	items, err := q.Claim(context.Background(), 1, time.Minute, now)
	if err != nil || len(items) != 1 {
		t.Fatalf("claim = %d items, %v", len(items), err)
	}
	retryAt := now.Add(10 * time.Minute)
	if err := q.Release(context.Background(), items[0], retryAt, errors.New("central service unavailable")); err != nil {
		t.Fatal(err)
	}
	items, err = q.Claim(context.Background(), 1, time.Minute, now.Add(9*time.Minute))
	if err != nil || len(items) != 0 {
		t.Fatalf("early retry claim = %d items, %v", len(items), err)
	}
	items, err = q.Claim(context.Background(), 1, time.Minute, retryAt)
	if err != nil || len(items) != 1 || items[0].Attempts != 1 {
		t.Fatalf("deferred retry = %#v, %v", items, err)
	}
}

func TestQueueConcurrentClaimsDoNotDuplicateAnItem(t *testing.T) {
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "collector.sqlite")
	firstQueue := openTestQueue(t, path)
	defer firstQueue.Close()
	secondQueue := openTestQueue(t, path)
	defer secondQueue.Close()
	if _, err := firstQueue.Enqueue(context.Background(), testObservation(now), now); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	claims := make(chan []Item, 2)
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for _, current := range []*Queue{firstQueue, secondQueue} {
		workers.Add(1)
		go func(current *Queue) {
			defer workers.Done()
			<-start
			items, err := current.Claim(context.Background(), 1, time.Minute, now)
			claims <- items
			errs <- err
		}(current)
	}
	close(start)
	workers.Wait()
	close(claims)
	close(errs)
	claimed := 0
	for items := range claims {
		claimed += len(items)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed %d copies of one queue item", claimed)
	}
}

func openTestQueue(t *testing.T, path string) *Queue {
	t.Helper()
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func testObservation(now time.Time) model.Observation {
	return model.Observation{
		SourceInstanceKey: "activitywatch:mac:aw-watcher-window",
		SourceKind:        "activitywatch",
		DeviceID:          "mac",
		SourceRecordKey:   "event-1",
		Kind:              "activitywatch.event.currentwindow",
		OccurredAt:        now.Add(-time.Minute),
		CollectedAt:       now,
		OriginalPayload:   json.RawMessage(`{"id":1,"duration":10}`),
	}
}
