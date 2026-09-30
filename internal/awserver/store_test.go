package awserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

var base = time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

func openStore(t *testing.T, path string, now *time.Time) (*Store, *queue.Queue) {
	t.Helper()
	q, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	store, err := Open(context.Background(), q, "mac-test", func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return store, q
}

func claimAll(t *testing.T, q *queue.Queue, now time.Time) []map[string]any {
	t.Helper()
	items, err := q.Claim(context.Background(), 1000, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	result := []map[string]any{}
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal(item.Observation.OriginalPayload, &payload); err != nil {
			t.Fatal(err)
		}
		payload["_key"] = item.Observation.SourceRecordKey
		payload["_instance"] = item.Observation.SourceInstanceKey
		result = append(result, payload)
		if err := q.Acknowledge(context.Background(), item, now); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func beat(t *testing.T, store *Store, at time.Time, data string, pulse float64) Event {
	t.Helper()
	event, err := store.Heartbeat(context.Background(), "aw-watcher-window_host", Event{Timestamp: at, Data: json.RawMessage(data)}, pulse)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestHeartbeatsMergeLikeActivityWatch(t *testing.T) {
	now := base
	store, q := openStore(t, filepath.Join(t.TempDir(), "q.sqlite3"), &now)
	if created, err := store.EnsureBucket(context.Background(), Bucket{ID: "aw-watcher-window_host", Type: "currentwindow"}); err != nil || !created {
		t.Fatal(created, err)
	}
	first := beat(t, store, base, `{"app":"Helium","title":"a"}`, 2)
	merged := beat(t, store, base.Add(time.Second), `{"title":"a","app":"Helium"}`, 2)
	if merged.ID != first.ID || merged.Duration != 1 {
		t.Fatalf("same data within pulsetime should extend the event: %+v", merged)
	}
	gap := beat(t, store, base.Add(10*time.Second), `{"app":"Helium","title":"a"}`, 2)
	if gap.ID == first.ID {
		t.Fatal("a heartbeat after the pulsetime gap must start a new event")
	}
	changed := beat(t, store, base.Add(11*time.Second), `{"app":"Helium","title":"b"}`, 2)
	if changed.ID == gap.ID {
		t.Fatal("changed data must start a new event")
	}
	now = base.Add(12 * time.Second)
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := claimAll(t, q, now)
	// first (queued at start, then its 1 s revision when closed), gap, changed.
	durations := map[string][]float64{}
	for _, record := range records {
		if record["_instance"] != "activitywatch:mac-test:aw-watcher-window_host" {
			t.Fatalf("instance = %v", record["_instance"])
		}
		key := record["_key"].(string)
		durations[key] = append(durations[key], record["duration"].(float64))
	}
	if got := durations["td:1"]; len(got) != 2 || got[1] != 1 {
		t.Fatalf("first event revisions = %v", got)
	}
	if len(durations) != 3 {
		t.Fatalf("records = %v", durations)
	}
}

func TestOpenEventSurvivesRestart(t *testing.T) {
	now := base
	path := filepath.Join(t.TempDir(), "q.sqlite3")
	store, q := openStore(t, path, &now)
	store.EnsureBucket(context.Background(), Bucket{ID: "aw-watcher-window_host"})
	first := beat(t, store, base, `{"app":"A","title":""}`, 2)
	q.Close()

	restarted, _ := openStore(t, path, &now)
	again := beat(t, restarted, base.Add(time.Second), `{"app":"A","title":""}`, 2)
	if again.ID != first.ID || again.Duration != 1 {
		t.Fatalf("restart lost the open event: %+v", again)
	}
	next := beat(t, restarted, base.Add(2*time.Second), `{"app":"B","title":""}`, 2)
	if next.ID <= first.ID {
		t.Fatalf("event ids must keep increasing across restarts: %d", next.ID)
	}
}

func TestInsertIsIdempotentPerEventKey(t *testing.T) {
	now := base
	store, q := openStore(t, filepath.Join(t.TempDir(), "q.sqlite3"), &now)
	store.EnsureBucket(context.Background(), Bucket{ID: "aw-watcher-codex_derived_host"})
	event := Event{Timestamp: base, Duration: 5, Data: json.RawMessage(`{"event_key":"codex:1","status":"running"}`)}
	for range 2 {
		if _, err := store.Insert(context.Background(), "aw-watcher-codex_derived_host", []Event{event}); err != nil {
			t.Fatal(err)
		}
	}
	event.Duration = 9
	event.Data = json.RawMessage(`{"event_key":"codex:1","status":"completed"}`)
	store.Insert(context.Background(), "aw-watcher-codex_derived_host", []Event{event})
	records := claimAll(t, q, now)
	if len(records) != 2 || records[0]["_key"] != "key:codex:1" || records[1]["_key"] != "key:codex:1" {
		t.Fatalf("want one record with two revisions, got %v", records)
	}
}

func TestHandlerServesExtensionAndRejectsWebPages(t *testing.T) {
	now := base
	store, q := openStore(t, filepath.Join(t.TempDir(), "q.sqlite3"), &now)
	server := httptest.NewServer(Handler{Store: store, Hostname: "host", Version: "test"})
	defer server.Close()
	do := func(method, path, origin, body string) *http.Response {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	const extension = "chrome-extension://nglaklhklhcoonedhgnpgddginnjdadi"
	bucket := "/api/0/buckets/aw-watcher-web-chrome_host"
	if r := do("POST", bucket+"/heartbeat?pulsetime=5", extension, `{"timestamp":"2026-09-30T08:00:00Z","duration":0,"data":{}}`); r.StatusCode != 404 {
		t.Fatalf("heartbeat to a missing bucket = %d, want 404 so the extension creates it", r.StatusCode)
	}
	if r := do("POST", bucket, extension, `{"client":"aw-client-web","type":"web.tab.current","hostname":"host"}`); r.StatusCode != 200 {
		t.Fatalf("create = %d", r.StatusCode)
	}
	if r := do("POST", bucket, extension, `{"client":"aw-client-web","type":"web.tab.current","hostname":"host"}`); r.StatusCode != 304 {
		t.Fatalf("second create = %d, want 304", r.StatusCode)
	}
	r := do("POST", bucket+"/heartbeat?pulsetime=5", extension, `{"timestamp":"2026-09-30T08:00:00.500Z","duration":0,"data":{"url":"https://x.com/home","title":"Home / X","audible":false,"incognito":false,"tabCount":3}}`)
	var event Event
	json.NewDecoder(r.Body).Decode(&event)
	if r.StatusCode != 200 || event.ID == 0 || event.Timestamp.IsZero() {
		t.Fatalf("heartbeat = %d %+v", r.StatusCode, event)
	}
	if r := do("POST", bucket+"/heartbeat?pulsetime=5", "https://evil.example", `{"timestamp":"2026-09-30T08:00:00Z","duration":0,"data":{}}`); r.StatusCode != 403 {
		t.Fatalf("web page origin = %d, want 403", r.StatusCode)
	}
	r = do("GET", "/api/0/info", "", "")
	var info map[string]any
	json.NewDecoder(r.Body).Decode(&info)
	if info["hostname"] != "host" {
		t.Fatalf("info = %v", info)
	}
	records := claimAll(t, q, now)
	if len(records) != 1 || records[0]["_instance"] != "activitywatch:mac-test:aw-watcher-web-chrome_host" {
		t.Fatalf("records = %v", records)
	}
}
