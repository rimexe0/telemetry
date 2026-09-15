package projectsource

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/queue"
	_ "modernc.org/sqlite"
)

func TestSyncCapturesSelectedMetadataWithoutMessages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
		INSERT INTO projection_projects(project_id,title,workspace_root,scripts_json,created_at,updated_at) VALUES ('p1','One','/repo/one','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z'), ('p2','Two','/repo/two','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z');
		INSERT INTO projection_threads(thread_id,project_id,title,branch,worktree_path,created_at,updated_at) VALUES ('t1','p1','private prompt title',NULL,NULL,'2026-09-12T08:20:00Z','2026-09-12T09:30:00Z'), ('t2','p2','other','main','/repo/two','2026-09-12T08:20:00Z','2026-09-12T09:30:00Z');
		INSERT INTO projection_turns(thread_id,turn_id,state,requested_at,started_at,completed_at,checkpoint_files_json) VALUES ('t1','turn1','completed','2026-09-12T09:30:00Z','2026-09-12T09:31:00Z','2026-09-12T09:40:00Z','{}');
		INSERT INTO projection_thread_sessions(thread_id,status,provider_name,updated_at) VALUES ('t1','stopped','codex','2026-09-12T09:35:00Z');
		INSERT INTO provider_session_runtime(thread_id,provider_name,adapter_key,status,last_seen_at) VALUES ('t1','codex','codex','stopped','2026-09-12T09:36:00Z');
		INSERT INTO projection_thread_messages(message_id,thread_id,role,text,is_streaming,created_at,updated_at) VALUES ('m1','t1','user','PRIVATE PROMPT MUST NOT BE READ',0,'2026-09-12T09:30:00Z','2026-09-12T09:30:00Z');
	`)

	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	result, err := Sync(context.Background(), q, Options{
		DeviceID:     "mac-test",
		DBPath:       dbPath,
		ProjectPaths: []string{"/repo/one"},
		From:         time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC),
		Until:        now,
		Now:          now,
		Limit:        100,
		CollectGit:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project == nil || len(result.Project) != 1 || result.Project[0] != "/repo/one" {
		t.Fatalf("matched projects = %#v", result.Project)
	}
	if result.State.Observed != 5 || result.State.RevisionsQueued != 5 || !result.State.CursorAdvanced {
		t.Fatalf("state result = %#v", result.State)
	}
	items, err := q.Claim(context.Background(), 20, time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("claimed %d metadata observations", len(items))
	}
	for _, item := range items {
		if strings.Contains(string(item.Observation.OriginalPayload), "PRIVATE PROMPT") {
			t.Fatalf("message content crossed the source boundary: %s", item.Observation.Kind)
		}
		if item.Observation.Kind == "t3code.thread" && strings.Contains(string(item.Observation.OriginalPayload), "private prompt title") {
			t.Fatal("thread title crossed the metadata-only boundary")
		}
		if err := q.Acknowledge(context.Background(), item, now); err != nil {
			t.Fatal(err)
		}
	}

	// A repeat with no source changes is idempotent, while a changed thread
	// state creates one retained revision under the same logical record key.
	repeat, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, Until: now.Add(5 * time.Minute), Now: now.Add(5 * time.Minute), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.State.RevisionsQueued != 0 {
		t.Fatalf("unchanged state queued %d revisions", repeat.State.RevisionsQueued)
	}
	mustExec(t, db, `UPDATE projection_threads SET branch='feature', updated_at='2026-09-12T10:03:00Z' WHERE thread_id='t1'`)
	changed, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, Until: now.Add(6 * time.Minute), Now: now.Add(6 * time.Minute), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if changed.State.RevisionsQueued != 1 {
		t.Fatalf("changed state queued %d revisions, want 1", changed.State.RevisionsQueued)
	}
}

func TestUsageCachePaginationAdvancesBoundedCursor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	defer db.Close()
	mustExec(t, db, `INSERT INTO projection_projects(project_id,title,workspace_root,scripts_json,created_at,updated_at) VALUES ('p1','One','/repo/one','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z')`)
	cachePath := filepath.Join(t.TempDir(), "usage-scan-cache.json")
	cache := map[string]any{
		"version": 3, "models": []string{"gpt-test"}, "sessions": []string{"secret-session"},
		"files": map[string]any{"rollout.jsonl": map[string]any{"p": "codex", "r": [][]any{
			{float64(1789203660000), float64(0), float64(0), float64(10), float64(0), float64(0), float64(2), float64(0), "one", nil},
			{float64(1789203720000), float64(0), float64(0), float64(11), float64(0), float64(0), float64(3), float64(0), "two", nil},
			{float64(1789203780000), float64(0), float64(0), float64(12), float64(0), float64(0), float64(4), float64(0), "three", nil},
			{float64(1789207200000), float64(0), float64(0), float64(13), float64(0), float64(0), float64(5), float64(0), "at-until", nil},
		}, "t": [][]any{}}},
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(cachePath, data); err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	from := time.UnixMilli(1789203600000).UTC()
	first, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, From: from, Until: from.Add(time.Hour), Now: from.Add(time.Hour), Limit: 2, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if first.Usage.Observed != 2 || !first.Usage.Truncated || !first.Usage.CursorAdvanced {
		t.Fatalf("first usage result = %#v", first.Usage)
	}
	items, err := q.Claim(context.Background(), 20, time.Minute, from.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if strings.Contains(string(item.Observation.OriginalPayload), "secret-session") {
			t.Fatal("raw provider session identifier crossed usage boundary")
		}
		if err := q.Acknowledge(context.Background(), item, from.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, Until: from.Add(2 * time.Hour), Now: from.Add(2 * time.Hour), Limit: 2, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if second.Usage.Observed != 2 || second.Usage.RevisionsQueued != 2 || second.Usage.Truncated || !second.Usage.CursorAdvanced {
		t.Fatalf("second usage result = %#v", second.Usage)
	}
}

func TestMalformedUsageRowsDoNotAdvanceCursor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	defer db.Close()
	mustExec(t, db, `INSERT INTO projection_projects(project_id,title,workspace_root,scripts_json,created_at,updated_at) VALUES ('p1','One','/repo/one','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z')`)
	cachePath := filepath.Join(t.TempDir(), "usage-scan-cache.json")
	valid := []any{float64(1789203660000), float64(0), float64(0), float64(10), float64(0), float64(0), float64(2), float64(0), "one", nil}
	writeCache := func(rows [][]any) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"version": 3, "models": []string{"gpt-test"}, "sessions": []string{"session"}, "files": map[string]any{"rollout": map[string]any{"p": "codex", "r": rows, "t": [][]any{}}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cachePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCache([][]any{valid, []any{}})
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	from := time.UnixMilli(1789203600000).UTC()
	first, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, From: from, Until: from.Add(time.Hour), Now: from.Add(time.Hour), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if first.Usage.Status != "partial" || first.Usage.CursorAdvanced || first.Usage.RevisionsQueued < 1 {
		t.Fatalf("malformed usage result = %#v", first.Usage)
	}
	writeCache([][]any{valid})
	second, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, From: from, Until: from.Add(2 * time.Hour), Now: from.Add(2 * time.Hour), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Usage.CursorAdvanced || second.Usage.RevisionsQueued != 0 {
		t.Fatalf("repaired usage result = %#v", second.Usage)
	}
}

func TestUsageCacheRequiresTopLevelShape(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	db.Close()
	cachePath := filepath.Join(t.TempDir(), "usage-scan-cache.json")
	if err := os.WriteFile(cachePath, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	_, err = Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, From: time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Now: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Limit: 10, CollectGit: false})
	if err == nil || !strings.Contains(err.Error(), "requires non-null models, sessions, and files") {
		t.Fatalf("invalid top-level shape error = %v", err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":3,"models":[],"sessions":[],"files":{"rollout":{"p":"codex","r":[],"t":null}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, UsageCachePath: cachePath, ProjectPaths: []string{"/repo/one"}, From: time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Now: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Limit: 10, CollectGit: false})
	if err == nil || !strings.Contains(err.Error(), "invalid provider or record arrays") {
		t.Fatalf("invalid file shape error = %v", err)
	}
}

func TestNullDedupeUsageRowsKeepDistinctStableKeys(t *testing.T) {
	row := []any{float64(1789203660000), float64(0), float64(0), float64(10), float64(0), float64(0), float64(2), float64(0), nil, nil}
	first, ok := parseUsageItem("rollout.jsonl", "codex", row, []string{"gpt-test"}, []string{"session"}, 0)
	if !ok {
		t.Fatal("first null-key usage row was rejected")
	}
	second, ok := parseUsageItem("rollout.jsonl", "codex", row, []string{"gpt-test"}, []string{"session"}, 1)
	if !ok {
		t.Fatal("second null-key usage row was rejected")
	}
	if first.Key == second.Key {
		t.Fatalf("null-key rows collapsed into %q", first.Key)
	}
}

func TestStatePaginationKeepsRowsWithTiedTimestamps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	defer db.Close()
	mustExec(t, db, `INSERT INTO projection_projects(project_id,title,workspace_root,scripts_json,created_at,updated_at) VALUES ('p1','One','/repo/one','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z')`)
	mustExec(t, db, `INSERT INTO projection_threads(thread_id,project_id,title,branch,worktree_path,created_at,updated_at) VALUES ('t1','p1','one','main','/repo/one','2026-09-12T09:00:00Z','2026-09-12T09:30:00Z'), ('t2','p1','two','main','/repo/one','2026-09-12T09:00:00Z','2026-09-12T09:30:00Z'), ('t3','p1','three','main','/repo/one','2026-09-12T09:00:00Z','2026-09-12T09:30:00Z')`)
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	from := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	until := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for index := 0; index < 4; index++ {
		result, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, From: func() time.Time {
			if index == 0 {
				return from
			}
			return time.Time{}
		}(), Until: until.Add(time.Duration(index) * time.Minute), Now: until.Add(time.Duration(index) * time.Minute), Limit: 1, CollectGit: false})
		if err != nil {
			t.Fatal(err)
		}
		if index < 2 && !result.State.Truncated {
			t.Fatalf("pass %d did not report a remaining tied row: %#v", index, result.State)
		}
		if index >= 2 && result.State.Truncated {
			t.Fatalf("final pass remained truncated: %#v", result.State)
		}
	}
	stats, err := q.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 4 {
		t.Fatalf("pending tied rows = %d, want project plus three thread revisions", stats.Pending)
	}
}

func TestUnfinishedTurnBeforeWatermarkIsRevisited(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db := openFixtureDB(t, dbPath)
	defer db.Close()
	mustExec(t, db, `INSERT INTO projection_projects(project_id,title,workspace_root,scripts_json,created_at,updated_at) VALUES ('p1','One','/repo/one','{}','2026-09-12T08:00:00Z','2026-09-12T08:10:00Z')`)
	mustExec(t, db, `INSERT INTO projection_threads(thread_id,project_id,title,branch,worktree_path,created_at,updated_at) VALUES ('t1','p1','one','main','/repo/one','2026-09-12T08:00:00Z','2026-09-12T09:30:00Z')`)
	mustExec(t, db, `INSERT INTO projection_turns(thread_id,turn_id,state,requested_at,started_at,checkpoint_files_json) VALUES ('t1','open','running','2026-09-12T08:00:00Z','2026-09-12T08:05:00Z','{}')`)
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	first, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, From: time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC), Until: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Now: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if first.State.Observed != 3 || first.State.RevisionsQueued != 3 {
		t.Fatalf("initial unfinished turn result = %#v", first.State)
	}
	second, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, Until: time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC), Now: time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if second.State.Observed != 2 || second.State.RevisionsQueued != 0 {
		t.Fatalf("unchanged unfinished turn result = %#v", second.State)
	}
	mustExec(t, db, `UPDATE projection_turns SET state='waiting' WHERE thread_id='t1' AND turn_id='open'`)
	third, err := Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: dbPath, ProjectPaths: []string{"/repo/one"}, Until: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), Now: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), Limit: 100, CollectGit: false})
	if err != nil {
		t.Fatal(err)
	}
	if third.State.RevisionsQueued != 1 {
		t.Fatalf("changed unfinished turn result = %#v", third.State)
	}
}

func TestParseGitStatusCountsWithoutReadingPaths(t *testing.T) {
	status := parseGitStatus("/repo", "main", "deadbeef", "## main...origin/main\nM  staged.go\n M work.go\n?? private.txt\nUU conflict.go\n")
	if status.Staged != 2 || status.Unstaged != 2 || status.Untracked != 1 || status.Conflicted != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestValidateQueuePathRejectsFileAliases(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "state.sqlite")
	if err := os.WriteFile(source, []byte("sqlite fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQueuePath(source, source); err == nil {
		t.Fatal("same queue/source path was accepted")
	}
	symlink := filepath.Join(root, "queue-symlink.sqlite")
	if err := os.Symlink(source, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQueuePath(source, symlink); err == nil {
		t.Fatal("symlinked queue/source path was accepted")
	}
	hardlink := filepath.Join(root, "queue-hardlink.sqlite")
	if err := os.Link(source, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQueuePath(source, hardlink); err == nil {
		t.Fatal("hard-linked queue/source path was accepted")
	}
	if err := ValidateQueuePath(source, filepath.Join(root, "new-queue.sqlite")); err != nil {
		t.Fatalf("unrelated queue path rejected: %v", err)
	}
	usage := filepath.Join(root, "usage-cache.json")
	if err := os.WriteFile(usage, []byte("usage fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	usageSymlink := filepath.Join(root, "usage-queue-symlink.sqlite")
	if err := os.Symlink(usage, usageSymlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQueuePaths(usageSymlink, usage); err == nil {
		t.Fatal("usage-cache symlink alias was accepted")
	}
	usageHardlink := filepath.Join(root, "usage-queue-hardlink.sqlite")
	if err := os.Link(usage, usageHardlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateQueuePaths(usageHardlink, usage); err == nil {
		t.Fatal("usage-cache hard-link alias was accepted")
	}
}

func TestSyncRejectsWhitespaceProjectPath(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "collector.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	_, err = Sync(context.Background(), q, Options{DeviceID: "mac-test", DBPath: "/does/not/need/to/open", ProjectPaths: []string{" /repo/one"}, Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "leading or trailing whitespace") {
		t.Fatalf("whitespace path error = %v", err)
	}
}

func TestGitStatusDisablesOptionalIndexLocks(t *testing.T) {
	configured := withGitOptionalLocksDisabled([]string{"PATH=/bin", "GIT_OPTIONAL_LOCKS=1"})
	if !containsValue(configured, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("configured environment = %#v", configured)
	}
	added := withGitOptionalLocksDisabled([]string{"PATH=/bin"})
	if !containsValue(added, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("missing environment entry = %#v", added)
	}
}

func openFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `
		CREATE TABLE projection_projects(project_id TEXT PRIMARY KEY,title TEXT NOT NULL,workspace_root TEXT NOT NULL,scripts_json TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT);
		CREATE TABLE projection_threads(thread_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,title TEXT NOT NULL,branch TEXT,worktree_path TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,runtime_mode TEXT NOT NULL DEFAULT 'full-access',interaction_mode TEXT NOT NULL DEFAULT 'default');
		CREATE TABLE projection_turns(row_id INTEGER PRIMARY KEY AUTOINCREMENT,thread_id TEXT NOT NULL,turn_id TEXT,pending_message_id TEXT,assistant_message_id TEXT,state TEXT NOT NULL,requested_at TEXT NOT NULL,started_at TEXT,completed_at TEXT,checkpoint_turn_count INTEGER,checkpoint_ref TEXT,checkpoint_status TEXT,checkpoint_files_json TEXT NOT NULL);
		CREATE TABLE projection_thread_sessions(thread_id TEXT PRIMARY KEY,status TEXT NOT NULL,provider_name TEXT,provider_session_id TEXT,provider_thread_id TEXT,active_turn_id TEXT,last_error TEXT,updated_at TEXT NOT NULL,runtime_mode TEXT NOT NULL DEFAULT 'full-access',provider_instance_id TEXT);
		CREATE TABLE provider_session_runtime(thread_id TEXT PRIMARY KEY,provider_name TEXT NOT NULL,adapter_key TEXT NOT NULL,runtime_mode TEXT NOT NULL DEFAULT 'full-access',status TEXT NOT NULL,last_seen_at TEXT NOT NULL,resume_cursor_json TEXT,runtime_payload_json TEXT,provider_instance_id TEXT);
		CREATE TABLE projection_thread_messages(message_id TEXT PRIMARY KEY,thread_id TEXT NOT NULL,turn_id TEXT,role TEXT NOT NULL,text TEXT NOT NULL,is_streaming INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,attachments_json TEXT);
	`)
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
