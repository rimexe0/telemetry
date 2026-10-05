package collector

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
)

func fakeT3(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "environment-id"), []byte("env-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TABLE projection_projects (project_id TEXT, title TEXT, workspace_root TEXT, created_at TEXT, updated_at TEXT, deleted_at TEXT)`,
		`CREATE TABLE projection_threads (thread_id TEXT, project_id TEXT, title TEXT, branch TEXT, worktree_path TEXT, created_at TEXT, updated_at TEXT,
			archived_at TEXT, deleted_at TEXT, latest_user_message_at TEXT, pending_approval_count INTEGER, pending_user_input_count INTEGER,
			runtime_mode TEXT, interaction_mode TEXT, model_selection_json TEXT)`,
		`CREATE TABLE projection_thread_sessions (thread_id TEXT, status TEXT, provider_name TEXT, active_turn_id TEXT, updated_at TEXT)`,
		`CREATE TABLE projection_turns (thread_id TEXT, turn_id TEXT, state TEXT, requested_at TEXT, started_at TEXT, completed_at TEXT)`,
		`INSERT INTO projection_projects VALUES ('p1', 'Telemetry', '/code/telemetry', '2026-09-01T10:00:00.000Z', '2026-09-01T10:00:00.000Z', NULL)`,
		`INSERT INTO projection_threads VALUES ('t1', 'p1', 'Fix the collector', 'main', NULL, '2026-09-30T09:00:00.000Z', '2026-09-30T10:00:00.000Z',
			NULL, NULL, '2026-09-30T10:00:00.000Z', 0, 0, 'full-access', 'default', '{"instanceId":"claudeAgent","model":"claude-opus-5-5"}')`,
		`INSERT INTO projection_thread_sessions VALUES ('t1', 'running', 'claudeAgent', 'u1', '2026-09-30T10:00:00.000Z')`,
		`INSERT INTO projection_turns VALUES ('t1', 'u1', 'running', '2026-09-30T10:00:00.000Z', '2026-09-30T10:00:01.000Z', NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return dir, db
}

func TestT3RecordsThreadsTurnsAndChangesOnly(t *testing.T) {
	dir, db := fakeT3(t)
	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac-1"}
	module := &T3{Dir: dir, queued: map[string]string{}}
	now := time.Date(2026, 9, 30, 10, 1, 0, 0, time.UTC)
	claim := func() map[string]map[string]any {
		items, err := q.Claim(context.Background(), 100, time.Minute, now)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]map[string]any{}
		for _, item := range items {
			if !strings.HasPrefix(item.Observation.SourceInstanceKey, "t3:mac-1:env-1") {
				t.Fatalf("instance = %s", item.Observation.SourceInstanceKey)
			}
			var payload map[string]any
			json.Unmarshal(item.Observation.OriginalPayload, &payload)
			found[item.Observation.SourceRecordKey] = payload
			q.Acknowledge(context.Background(), item, now)
		}
		return found
	}
	if err := module.collect(context.Background(), host, now); err != nil {
		t.Fatal(err)
	}
	first := claim()
	if len(first) != 3 || first["thread:t1"]["title"] != "Fix the collector" || first["thread:t1"]["model"] != "claude-opus-5-5" || first["turn:t1:u1"]["state"] != "running" {
		t.Fatalf("first = %+v", first)
	}
	if err := module.collect(context.Background(), host, now); err != nil {
		t.Fatal(err)
	}
	if again := claim(); len(again) != 0 {
		t.Fatalf("unchanged rows were queued again: %+v", again)
	}
	db.Exec(`UPDATE projection_turns SET state = 'completed', completed_at = '2026-09-30T10:05:00.000Z'`)
	db.Exec(`UPDATE projection_threads SET pending_approval_count = 1, updated_at = '2026-09-30T10:05:00.000Z'`)
	if err := module.collect(context.Background(), host, now); err != nil {
		t.Fatal(err)
	}
	changed := claim()
	if len(changed) != 2 || changed["turn:t1:u1"]["state"] != "completed" || changed["thread:t1"]["pending_approval_count"] != float64(1) {
		t.Fatalf("changed = %+v", changed)
	}
	for _, key := range []string{"messages", "text", "content"} {
		if _, ok := changed["thread:t1"][key]; ok {
			t.Fatalf("message content must not be recorded (%s)", key)
		}
	}
}

func TestT3ReportsASchemaChangeInsteadOfGuessing(t *testing.T) {
	dir, db := fakeT3(t)
	db.Exec(`DROP TABLE projection_turns`)
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	module := &T3{Dir: dir, queued: map[string]string{}}
	err := module.collect(context.Background(), Host{Queue: q, DeviceID: "mac-1"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "schema may have changed") {
		t.Fatalf("err = %v", err)
	}
}

// fakeT3V2 is a T3 userdata directory after the move to orchestration v2:
// state.sqlite is left behind and statev2.sqlite holds the current state.
func fakeT3V2(t *testing.T) string {
	t.Helper()
	dir, _ := fakeT3(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "statev2.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE projection_projects (project_id TEXT, title TEXT, workspace_root TEXT, created_at TEXT, updated_at TEXT, deleted_at TEXT)`,
		`CREATE TABLE orchestration_v2_projection_threads (thread_id TEXT, project_id TEXT, title TEXT, default_provider TEXT, runtime_mode TEXT,
			interaction_mode TEXT, active_provider_thread_id TEXT, created_at TEXT, updated_at TEXT, archived_at TEXT, deleted_at TEXT, payload_json TEXT)`,
		`CREATE TABLE orchestration_v2_projection_runs (run_id TEXT, thread_id TEXT, ordinal INTEGER, provider TEXT, provider_thread_id TEXT, status TEXT,
			requested_at TEXT, completed_at TEXT, payload_json TEXT)`,
		`CREATE TABLE orchestration_v2_projection_messages (message_id TEXT, thread_id TEXT, run_id TEXT, node_id TEXT, role TEXT, streaming INTEGER,
			created_at TEXT, updated_at TEXT, payload_json TEXT)`,
		`CREATE TABLE orchestration_v2_projection_runtime_requests (runtime_request_id TEXT, thread_id TEXT, node_id TEXT, provider_turn_id TEXT,
			kind TEXT, status TEXT, created_at TEXT, resolved_at TEXT, payload_json TEXT)`,
		`CREATE TABLE orchestration_v2_projection_provider_sessions (provider_session_id TEXT, thread_id TEXT, provider TEXT, status TEXT, model TEXT,
			updated_at TEXT, payload_json TEXT)`,
		`INSERT INTO projection_projects VALUES ('p1', 'Telemetry', '/code/telemetry', '2026-09-01T10:00:00.000Z', '2026-09-01T10:00:00.000Z', NULL)`,
		`INSERT INTO orchestration_v2_projection_threads VALUES ('t1', 'p1', 'Fix the collector', 'claudeAgent', 'full-access', 'default', NULL,
			'2026-09-30T09:00:00.000Z', '2026-10-05T10:00:00.000Z', NULL, NULL,
			'{"branch":"main","worktreePath":null,"modelSelection":{"instanceId":"claudeAgent","model":"claude-opus-5-5"}}')`,
		`INSERT INTO orchestration_v2_projection_messages VALUES ('m1', 't1', 'r1', NULL, 'user', 0, '2026-10-05T09:00:00.000Z', '2026-10-05T09:00:00.000Z', '{"text":"secret"}')`,
		`INSERT INTO orchestration_v2_projection_messages VALUES ('m2', 't1', 'r2', NULL, 'user', 0, '2026-10-05T10:00:00.000Z', '2026-10-05T10:00:00.000Z', '{"text":"secret"}')`,
		`INSERT INTO orchestration_v2_projection_runtime_requests VALUES ('q1', 't1', 'n1', NULL, 'user_input', 'pending', '2026-10-05T10:01:00.000Z', NULL, '{}')`,
		`INSERT INTO orchestration_v2_projection_runtime_requests VALUES ('q2', 't1', 'n1', NULL, 'command', 'resolved', '2026-10-05T09:01:00.000Z', NULL, '{}')`,
		`INSERT INTO orchestration_v2_projection_provider_sessions VALUES ('s1', 't1', 'claudeAgent', 'closed', NULL, '2026-10-04T10:00:00.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_provider_sessions VALUES ('s2', 't1', 'claudeAgent', 'running', NULL, '2026-10-05T10:00:00.000Z', '{}')`,
		`INSERT INTO orchestration_v2_projection_runs VALUES ('r1', 't1', 1, 'claudeAgent', NULL, 'completed', '2026-10-05T09:00:00.000Z', '2026-10-05T09:20:00.000Z',
			'{"startedAt":"2026-10-05T09:00:01.000Z"}')`,
		`INSERT INTO orchestration_v2_projection_runs VALUES ('r2', 't1', 2, 'claudeAgent', NULL, 'waiting', '2026-10-05T10:00:00.000Z', NULL,
			'{"startedAt":"2026-10-05T10:00:01.000Z"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestT3ReadsOrchestrationV2WhenPresent(t *testing.T) {
	dir := fakeT3V2(t)
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	module := &T3{Dir: dir, queued: map[string]string{}}
	now := time.Date(2026, 10, 5, 10, 5, 0, 0, time.UTC)
	if err := module.collect(context.Background(), Host{Queue: q, DeviceID: "mac-1"}, now); err != nil {
		t.Fatal(err)
	}
	items, _ := q.Claim(context.Background(), 100, time.Minute, now)
	found := map[string]map[string]any{}
	for _, item := range items {
		if item.Observation.SourceSchemaVersion != t3SchemaVersionV2 {
			t.Fatalf("schema = %s", item.Observation.SourceSchemaVersion)
		}
		var payload map[string]any
		json.Unmarshal(item.Observation.OriginalPayload, &payload)
		found[item.Observation.SourceRecordKey] = payload
	}
	thread := found["thread:t1"]
	if len(found) != 4 || thread["title"] != "Fix the collector" || thread["branch"] != "main" || thread["model"] != "claude-opus-5-5" ||
		thread["latest_user_message_at"] != "2026-10-05T10:00:00.000Z" || thread["pending_user_input_count"] != float64(1) ||
		thread["pending_approval_count"] != float64(0) || thread["session_status"] != "running" || thread["active_turn_id"] != "r2" {
		t.Fatalf("found = %+v", found)
	}
	if done, waiting := found["turn:t1:r1"], found["turn:t1:r2"]; done["state"] != "completed" || done["started_at"] != "2026-10-05T09:00:01.000Z" ||
		waiting["state"] != "running" || waiting["run_status"] != "waiting" {
		t.Fatalf("turns = %+v / %+v", done, waiting)
	}
	for _, payload := range found {
		for _, value := range payload {
			if value == "secret" {
				t.Fatal("message content must not be recorded")
			}
		}
	}
}
