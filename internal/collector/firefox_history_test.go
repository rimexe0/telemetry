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

func writePlaces(t *testing.T, file string, visits [][3]any) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		// Firefox keeps the database in WAL mode.
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS moz_places (id INTEGER PRIMARY KEY, url LONGVARCHAR, title LONGVARCHAR)`,
		`CREATE TABLE IF NOT EXISTS moz_historyvisits (id INTEGER PRIMARY KEY, from_visit INTEGER, place_id INTEGER, visit_date INTEGER, visit_type INTEGER, session INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, visit := range visits {
		id, at := visit[0].(int), visit[1].(time.Time)
		var title any = visit[2].(string)
		if title == "" {
			title = nil
		}
		if _, err := db.Exec(`INSERT OR REPLACE INTO moz_places (id, url, title) VALUES (?, ?, ?)`, id, "https://example.com/"+visit[2].(string), title); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO moz_historyvisits (id, from_visit, place_id, visit_date, visit_type, session) VALUES (?, 0, ?, ?, 1, 0)`,
			id, id, at.UnixMicro()); err != nil {
			t.Fatal(err)
		}
	}
}

// LibreWolf's history is read like Helium's: each visit once, from every
// profile profiles.ini names, while the browser holds the file open.
func TestLibreWolfHistoryReadsEveryVisitOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "librewolf")
	profile := filepath.Join(root, "Profiles", "abcd.default-release")
	elsewhere := filepath.Join(t.TempDir(), "work-profile")
	for _, dir := range []string{profile, elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ini := "[General]\nStartWithLastProfile=1\n\n[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/abcd.default-release\nDefault=1\n\n" +
		"[Profile1]\nName=work\nIsRelative=0\nPath=" + elsewhere + "\n"
	os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644)
	at := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	writePlaces(t, filepath.Join(profile, "places.sqlite"), [][3]any{{1, at, "Krita docs"}, {2, at.Add(time.Minute), ""}})
	writePlaces(t, filepath.Join(elsewhere, "places.sqlite"), [][3]any{{1, at.Add(2 * time.Minute), "Moko"}})

	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac-1"}
	history := &BrowserHistory{FirefoxRoots: []string{root}}
	ctx := context.Background()
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	if err := history.collect(ctx, host, now); err != nil {
		t.Fatal(err)
	}
	writePlaces(t, filepath.Join(profile, "places.sqlite"), [][3]any{{3, at.Add(3 * time.Minute), "Clip Studio"}})
	if err := history.collect(ctx, host, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	items, err := q.Claim(ctx, 100, time.Minute, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	instances := map[string]int{}
	for _, item := range items {
		instances[item.Observation.SourceInstanceKey]++
	}
	if instances["browser-history:mac-1:librewolf:abcd.default-release"] != 3 || instances["browser-history:mac-1:librewolf:work-profile"] != 1 {
		t.Fatalf("instances = %v", instances)
	}
	first := items[0].Observation
	var payload map[string]any
	json.Unmarshal(first.OriginalPayload, &payload)
	if first.Kind != "browser.visit" || first.SourceRecordKey != "visit:1" || !first.OccurredAt.Equal(at) || first.EndedAt != nil ||
		payload["title"] != "Krita docs" || payload["url"] != "https://example.com/Krita docs" || first.SourceSchemaVersion != "firefox-places-1" {
		t.Fatalf("first = %+v %s", first, first.OriginalPayload)
	}
	if status := history.Status(); status["visits_sent"] != 4 || status["history_files"] != 2 {
		t.Fatalf("status = %v", status)
	}
}
