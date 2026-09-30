package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
	"github.com/rimexe0/telemetry/internal/queue"
)

type fakePlatform struct {
	window Window
	idle   time.Duration
	locked bool
}

func (f *fakePlatform) ActiveWindow() (Window, bool, error) { return f.window, f.window.App != "", nil }
func (f *fakePlatform) IdleTime() (time.Duration, error)    { return f.idle, nil }
func (f *fakePlatform) ScreenLocked() (bool, error)         { return f.locked, nil }
func (f *fakePlatform) CanReadTitles(bool) bool             { return true }

type span struct {
	status   string
	start    time.Time
	duration float64
}

func TestAFKSpansFollowLastInputWithoutOverlap(t *testing.T) {
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	now := base
	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	clock := func() time.Time { return now }
	store, err := awserver.Open(context.Background(), q, "mac-test", clock)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Hostname: "host", Now: clock}
	options.defaults()
	store.EnsureBucket(context.Background(), awserver.Bucket{ID: options.AFKBucket})
	platform := &fakePlatform{}
	watcher := &afkWatcher{store: store, platform: platform, options: options}
	// Active for a minute, idle for five, then back; then a lock.
	for second := 0; second <= 60; second += 5 {
		now, platform.idle = base.Add(time.Duration(second)*time.Second), time.Second
		watcher.sample(context.Background())
	}
	for second := 65; second <= 360; second += 5 {
		now, platform.idle = base.Add(time.Duration(second)*time.Second), time.Duration(second-59)*time.Second
		watcher.sample(context.Background())
	}
	now, platform.idle = base.Add(365*time.Second), 0
	watcher.sample(context.Background())
	now, platform.idle, platform.locked = base.Add(370*time.Second), 2*time.Second, true
	watcher.sample(context.Background())
	store.Flush(context.Background())

	items, _ := q.Claim(context.Background(), 1000, time.Minute, now)
	latest := map[string]span{}
	order := []string{}
	for _, item := range items {
		var event struct {
			Timestamp time.Time
			Duration  float64
			Data      struct{ Status string }
		}
		json.Unmarshal(item.Observation.OriginalPayload, &event)
		if _, seen := latest[item.Observation.SourceRecordKey]; !seen {
			order = append(order, item.Observation.SourceRecordKey)
		}
		latest[item.Observation.SourceRecordKey] = span{event.Data.Status, event.Timestamp, event.Duration}
	}
	sort.Slice(order, func(i, j int) bool { return latest[order[i]].start.Before(latest[order[j]].start) })
	statuses := []string{}
	var previousEnd time.Time
	for _, key := range order {
		s := latest[key]
		statuses = append(statuses, s.status)
		if s.start.Before(previousEnd) {
			t.Fatalf("%s at %s overlaps the previous span ending %s", s.status, s.start, previousEnd)
		}
		previousEnd = s.start.Add(time.Duration(s.duration * float64(time.Second)))
	}
	want := []string{"not-afk", "afk", "not-afk", "afk"}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %v, want %v", statuses, want)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("statuses = %v, want %v", statuses, want)
		}
	}
	afk := latest[order[1]]
	if afk.start.Sub(base).Round(10*time.Millisecond) != 59*time.Second {
		t.Fatalf("afk should start at the last input (59 s), got %s", afk.start.Sub(base))
	}
}
