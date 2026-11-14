package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
)

func writeHistory(t *testing.T, file string, visits [][3]any) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT)`,
		`CREATE TABLE IF NOT EXISTS visits (id INTEGER PRIMARY KEY, url INTEGER, visit_time INTEGER, from_visit INTEGER, transition INTEGER, visit_duration INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, visit := range visits {
		id, at, title := visit[0].(int), visit[1].(time.Time), visit[2].(string)
		if _, err := db.Exec(`INSERT OR REPLACE INTO urls (id, url, title) VALUES (?, ?, ?)`, id, "https://example.com/"+title, title); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO visits (id, url, visit_time, from_visit, transition, visit_duration) VALUES (?, ?, ?, 0, 1, 5000000)`,
			id, id, at.UnixMicro()+chromeEpochOffset); err != nil {
			t.Fatal(err)
		}
	}
}

// Visits made while the collector was not running are read when it runs
// again, each once.
func TestBrowserHistoryReadsEveryVisitOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "net.imput.helium")
	if err := os.MkdirAll(filepath.Join(root, "Profile 1"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "Profile 1", "History")
	at := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	writeHistory(t, file, [][3]any{{1, at, "Krita docs"}, {2, at.Add(time.Minute), "Clip Studio"}})

	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac-1"}
	history := &BrowserHistory{Roots: []string{root}}
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if err := history.collect(ctx, host, now); err != nil {
		t.Fatal(err)
	}
	writeHistory(t, file, [][3]any{{3, at.Add(2 * time.Minute), "Moko"}})
	if err := history.collect(ctx, host, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	items, err := q.Claim(ctx, 100, time.Minute, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	first := items[0].Observation
	var payload map[string]any
	json.Unmarshal(first.OriginalPayload, &payload)
	if first.SourceInstanceKey != "browser-history:mac-1:net.imput.helium:Profile 1" || first.Kind != "browser.visit" || first.SourceRecordKey != "visit:1" ||
		!first.OccurredAt.Equal(at) || first.EndedAt == nil || !first.EndedAt.Equal(at.Add(5*time.Second)) || payload["title"] != "Krita docs" {
		t.Fatalf("first = %+v %s", first, first.OriginalPayload)
	}
	if status := history.Status(); status["visits_sent"] != 3 || status["history_files"] != 1 {
		t.Fatalf("status = %v", status)
	}
}
