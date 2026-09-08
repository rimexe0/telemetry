package activitywatch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

func TestSyncBucketUsesOverlapAndPreservesMutableRevision(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/0/buckets/window/events" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		requests++
		writer.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			writer.Write([]byte(`[{"id":1,"timestamp":"2026-09-07T11:55:00Z","duration":60,"data":{"app":"one"}}]`))
			return
		}
		if got := request.URL.Query().Get("start"); got != "2026-09-07T11:50:00Z" {
			t.Fatalf("overlap start = %s", got)
		}
		writer.Write([]byte(`[{"id":1,"timestamp":"2026-09-07T11:55:00Z","duration":120,"data":{"app":"one"}}]`))
	}))
	defer server.Close()
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	from := now.Add(-10 * time.Minute)
	options := SyncOptions{DeviceID: "mac", Bucket: Bucket{ID: "window"}, From: &from, Overlap: 10 * time.Minute, Limit: 100, Now: now}
	first, err := SyncBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, options)
	if err != nil || first.RevisionsEnqueued != 1 || !first.CursorAdvanced {
		t.Fatalf("first sync = %+v, %v", first, err)
	}
	options.From = nil
	options.Now = now.Add(5 * time.Minute)
	second, err := SyncBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, options)
	if err != nil || second.RevisionsEnqueued != 1 || !second.CursorAdvanced {
		t.Fatalf("second sync = %+v, %v", second, err)
	}
	stats, err := localQueue.Stats(context.Background())
	if err != nil || stats.Pending != 2 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestSyncBucketDoesNotAdvanceTruncatedWindow(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte(`[{"id":1,"timestamp":"2026-09-07T11:55:00Z","duration":1,"data":{}},{"id":2,"timestamp":"2026-09-07T11:56:00Z","duration":1,"data":{}}]`))
	}))
	defer server.Close()
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	from := now.Add(-10 * time.Minute)
	result, err := SyncBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, SyncOptions{DeviceID: "mac", Bucket: Bucket{ID: "window"}, From: &from, Limit: 2, Now: now})
	if err != nil || !result.Truncated || result.CursorAdvanced {
		t.Fatalf("sync = %+v, %v", result, err)
	}
}

func TestBackfillBucketUsesBoundedWindowsAndResumes(t *testing.T) {
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if got := request.URL.Query().Get("end"); got != start.Add(time.Duration(requests)*time.Hour).Format(time.RFC3339Nano) {
			t.Fatalf("request %d end = %s", requests, got)
		}
		if requests == 2 {
			if got := request.URL.Query().Get("start"); got != start.Add(55*time.Minute).Format(time.RFC3339Nano) {
				t.Fatalf("overlap start = %s", got)
			}
		}
		writer.Write([]byte(fmt.Sprintf(`[{"id":%d,"timestamp":"%s","duration":1,"data":{}}]`, requests, start.Add(time.Duration(requests)*time.Hour).Format(time.RFC3339))))
	}))
	defer server.Close()
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	collectedAt := end.Add(30 * time.Minute)
	result, err := BackfillBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, BackfillOptions{
		DeviceID: "mac", Bucket: Bucket{ID: "window"}, From: start, To: end,
		Window: time.Hour, Overlap: 5 * time.Minute, Limit: 100, Now: collectedAt,
	})
	if err != nil || result.WindowsSynced != 2 || result.EventsSeen != 2 || result.RevisionsEnqueued != 2 {
		t.Fatalf("backfill = %+v, %v", result, err)
	}
	items, err := localQueue.Claim(context.Background(), 10, time.Minute, collectedAt)
	if err != nil || len(items) != 2 || !items[0].Observation.CollectedAt.Equal(collectedAt) {
		t.Fatalf("queued collection time = %#v, %v", items, err)
	}
	result, err = BackfillBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, BackfillOptions{
		DeviceID: "mac", Bucket: Bucket{ID: "window"}, From: start, To: end,
		Window: time.Hour, Overlap: 5 * time.Minute, Limit: 100, Now: collectedAt,
	})
	if err != nil || !result.AlreadyCovered || requests != 2 {
		t.Fatalf("resumed backfill = %+v, requests=%d, %v", result, requests, err)
	}
}

func TestBackfillBucketNarrowsAFullWindowWithoutSkippingIt(t *testing.T) {
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		parsedStart, err := time.Parse(time.RFC3339Nano, request.URL.Query().Get("start"))
		if err != nil {
			t.Fatal(err)
		}
		parsedEnd, err := time.Parse(time.RFC3339Nano, request.URL.Query().Get("end"))
		if err != nil {
			t.Fatal(err)
		}
		if parsedEnd.Sub(parsedStart) > time.Hour {
			writer.Write([]byte(`[{"id":1,"timestamp":"2026-09-07T10:30:00Z","duration":1,"data":{}},{"id":2,"timestamp":"2026-09-07T11:30:00Z","duration":1,"data":{}}]`))
			return
		}
		if parsedEnd.Equal(start.Add(time.Hour)) {
			writer.Write([]byte(`[{"id":1,"timestamp":"2026-09-07T10:30:00Z","duration":1,"data":{}}]`))
			return
		}
		writer.Write([]byte(`[{"id":2,"timestamp":"2026-09-07T11:30:00Z","duration":1,"data":{}}]`))
	}))
	defer server.Close()
	localQueue, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer localQueue.Close()
	result, err := BackfillBucket(context.Background(), Client{BaseURL: server.URL}, localQueue, BackfillOptions{
		DeviceID: "mac", Bucket: Bucket{ID: "window"}, From: start, To: end,
		Window: 2 * time.Hour, Limit: 2, Now: end,
	})
	if err != nil || result.WindowsSynced != 2 || result.WindowsNarrowed != 1 || result.RevisionsEnqueued != 2 || requests != 3 {
		t.Fatalf("backfill = %+v, requests=%d, err=%v", result, requests, err)
	}
}
