package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	_ "modernc.org/sqlite"
)

// T3 reads the local T3 Code server's state database read-only and records
// its projects, threads and turns. Every client (desktop, web, phone) works
// through a T3 server, so reading each machine's server covers them all.
// Message contents are never read.
//
// T3 Code moved its state to statev2.sqlite (orchestration v2) in October
// 2026: threads and agent runs live in new tables there, and state.sqlite
// stops changing. The newer database is read when it exists.
//
// With an MCP credential in MCPTokenFile, it reads T3 through T3's MCP
// server instead (see t3MCP): the interface T3 supports for outside tools,
// which also tells the owner's messages from agents'.
type T3 struct {
	// Dir is T3's userdata directory, normally ~/.t3/userdata.
	Dir      string
	Interval time.Duration
	// MCPTokenFile holds a read-only T3 MCP credential; without it the
	// database is read.
	MCPTokenFile string

	mcp    *t3MCP
	source string

	health Health
	// queued is the digest last queued per record, so unchanged rows are
	// skipped without touching the queue.
	queued  map[string]string
	mu      sync.Mutex
	counts  map[string]int
	checked time.Time
}

const (
	t3SchemaVersion   = "t3-state-projection-1"
	t3SchemaVersionV2 = "t3-state-projection-2"
)

// t3Database is one layout of T3's state database and how to read it.
type t3Database struct {
	file    string
	version string
	readers []func(context.Context, *sql.DB) ([]t3Row, error)
}

var t3Databases = []t3Database{
	{"statev2.sqlite", t3SchemaVersionV2, []func(context.Context, *sql.DB) ([]t3Row, error){readT3Projects, readT3ThreadsV2, readT3RunsV2}},
	{"state.sqlite", t3SchemaVersion, []func(context.Context, *sql.DB) ([]t3Row, error){readT3Projects, readT3Threads, readT3Turns}},
}

func (*T3) Name() string { return "t3" }

func (t *T3) Run(ctx context.Context, host Host) error {
	if t.Interval == 0 {
		t.Interval = time.Minute
	}
	t.queued = map[string]string{}
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	for {
		if err := t.collect(ctx, host, time.Now().UTC()); err != nil {
			t.health.Failed(time.Now(), err)
		} else {
			t.health.Succeeded(time.Now())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (t *T3) Status() map[string]any {
	status := t.health.Fields()
	status["dir"] = t.Dir
	t.mu.Lock()
	defer t.mu.Unlock()
	status["source"] = t.source
	if !t.checked.IsZero() {
		status["rows"] = t.counts
	}
	return status
}

func (t *T3) collect(ctx context.Context, host Host, now time.Time) error {
	if token, err := os.ReadFile(t.MCPTokenFile); t.MCPTokenFile != "" && err == nil {
		return t.collectMCP(ctx, host, now, strings.TrimSpace(string(token)))
	}
	var database t3Database
	var path string
	for _, candidate := range t3Databases {
		path = filepath.Join(t.Dir, candidate.file)
		if _, err := os.Stat(path); err == nil {
			database = candidate
			break
		}
	}
	if database.file == "" {
		return fmt.Errorf("T3 state database: none of statev2.sqlite or state.sqlite in %s", t.Dir)
	}
	environment, err := os.ReadFile(filepath.Join(t.Dir, "environment-id"))
	if err != nil {
		return fmt.Errorf("T3 environment id: %w", err)
	}
	instance := "t3:" + host.DeviceID + ":" + strings.TrimSpace(string(environment))
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro&_pragma=busy_timeout(3000)&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	rows := []t3Row{}
	for _, read := range database.readers {
		found, err := read(ctx, db)
		if err != nil {
			return fmt.Errorf("read T3 state (its schema may have changed in an update): %w", err)
		}
		rows = append(rows, found...)
	}
	return t.record(ctx, host, now, instance, database.version, "database", rows)
}

// collectMCP reads T3 through its MCP server. A T3 that is not running has
// nothing new to say, which is not a failure.
func (t *T3) collectMCP(ctx context.Context, host Host, now time.Time, token string) error {
	environment, err := os.ReadFile(filepath.Join(t.Dir, "environment-id"))
	if err != nil {
		return fmt.Errorf("T3 environment id: %w", err)
	}
	instance := "t3:" + host.DeviceID + ":" + strings.TrimSpace(string(environment))
	origin, err := t3Origin(t.Dir)
	if err != nil {
		return nil
	}
	if t.mcp == nil || t.mcp.origin != origin || t.mcp.token != token {
		t.mcp = newT3MCP(origin, token)
	}
	rows, err := t.mcp.read(ctx)
	if errors.Is(err, errT3NotRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read T3 through its MCP server: %w", err)
	}
	return t.record(ctx, host, now, instance, t3SchemaVersionMCP, "mcp", rows)
}

// record queues the rows that changed since they were last queued.
func (t *T3) record(ctx context.Context, host Host, now time.Time, instance, version, source string, rows []t3Row) error {
	counts := map[string]int{}
	for _, row := range rows {
		payload, err := json.Marshal(row.payload)
		if err != nil {
			return err
		}
		observation := model.Observation{
			SourceInstanceKey:   instance,
			SourceKind:          "t3",
			DeviceID:            host.DeviceID,
			SourceRecordKey:     row.key,
			Kind:                row.kind,
			OccurredAt:          row.occurred,
			EndedAt:             row.ended,
			CollectedAt:         now,
			SourceSchemaVersion: version,
			OriginalPayload:     payload,
		}
		if err := observation.Normalize(); err != nil {
			return fmt.Errorf("%s %s: %w", row.kind, row.key, err)
		}
		counts[row.kind]++
		if t.queued[row.key] == observation.RevisionDigest {
			continue
		}
		if _, err := host.Queue.Enqueue(ctx, observation, now); err != nil {
			return err
		}
		t.queued[row.key] = observation.RevisionDigest
	}
	t.mu.Lock()
	t.counts, t.checked, t.source = counts, now, source
	t.mu.Unlock()
	return nil
}

type t3Row struct {
	kind     string
	key      string
	occurred time.Time
	ended    *time.Time
	payload  map[string]any
}

func parseT3Time(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("time %q: %w", value.String, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func text(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func readT3Projects(ctx context.Context, db *sql.DB) ([]t3Row, error) {
	result, err := db.QueryContext(ctx, `SELECT project_id, title, workspace_root, created_at, updated_at, deleted_at FROM projection_projects`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []t3Row{}
	for result.Next() {
		var id, title, root, created, updated, deleted sql.NullString
		if err := result.Scan(&id, &title, &root, &created, &updated, &deleted); err != nil {
			return nil, err
		}
		createdAt, err := parseT3Time(created)
		if err != nil || createdAt == nil {
			return nil, errors.Join(errors.New("project without created_at"), err)
		}
		rows = append(rows, t3Row{kind: "t3.project", key: "project:" + id.String, occurred: *createdAt, payload: map[string]any{
			"project_id": id.String, "title": text(title), "workspace_root": text(root),
			"created_at": text(created), "updated_at": text(updated), "deleted_at": text(deleted),
		}})
	}
	return rows, result.Err()
}

func readT3Threads(ctx context.Context, db *sql.DB) ([]t3Row, error) {
	result, err := db.QueryContext(ctx, `
		SELECT t.thread_id, t.project_id, t.title, t.branch, t.worktree_path, t.created_at, t.updated_at, t.archived_at, t.deleted_at,
			t.latest_user_message_at, t.pending_approval_count, t.pending_user_input_count, t.runtime_mode, t.interaction_mode,
			t.model_selection_json, s.status, s.provider_name, s.active_turn_id, s.updated_at
		FROM projection_threads t LEFT JOIN projection_thread_sessions s ON s.thread_id = t.thread_id`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []t3Row{}
	for result.Next() {
		// updated_at values keep a row that returns to an earlier state (such
		// as pending approvals 0, 1, 0) from repeating an earlier revision.
		var id, project, title, branch, worktree, created, updated, archived, deleted, latestUser, runtime, interaction, selection, status, provider, activeTurn, sessionUpdated sql.NullString
		var approvals, inputs sql.NullInt64
		if err := result.Scan(&id, &project, &title, &branch, &worktree, &created, &updated, &archived, &deleted, &latestUser,
			&approvals, &inputs, &runtime, &interaction, &selection, &status, &provider, &activeTurn, &sessionUpdated); err != nil {
			return nil, err
		}
		createdAt, err := parseT3Time(created)
		if err != nil || createdAt == nil {
			return nil, errors.Join(errors.New("thread without created_at"), err)
		}
		var model struct {
			InstanceID string `json:"instanceId"`
			Model      string `json:"model"`
		}
		if selection.Valid {
			_ = json.Unmarshal([]byte(selection.String), &model)
		}
		rows = append(rows, t3Row{kind: "t3.thread", key: "thread:" + id.String, occurred: *createdAt, payload: map[string]any{
			"thread_id": id.String, "project_id": text(project), "title": text(title), "branch": text(branch), "worktree_path": text(worktree),
			"created_at": text(created), "updated_at": text(updated), "archived_at": text(archived), "deleted_at": text(deleted),
			"latest_user_message_at": text(latestUser), "pending_approval_count": approvals.Int64, "pending_user_input_count": inputs.Int64,
			"runtime_mode": text(runtime), "interaction_mode": text(interaction), "provider_instance": model.InstanceID, "model": model.Model,
			"session_status": text(status), "session_provider": text(provider), "active_turn_id": text(activeTurn), "session_updated_at": text(sessionUpdated),
		}})
	}
	return rows, result.Err()
}

func readT3Turns(ctx context.Context, db *sql.DB) ([]t3Row, error) {
	result, err := db.QueryContext(ctx, `SELECT thread_id, turn_id, state, requested_at, started_at, completed_at FROM projection_turns WHERE turn_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []t3Row{}
	for result.Next() {
		var thread, turn, state, requested, started, completed sql.NullString
		if err := result.Scan(&thread, &turn, &state, &requested, &started, &completed); err != nil {
			return nil, err
		}
		requestedAt, err := parseT3Time(requested)
		if err != nil || requestedAt == nil {
			return nil, errors.Join(errors.New("turn without requested_at"), err)
		}
		completedAt, err := parseT3Time(completed)
		if err != nil {
			return nil, err
		}
		rows = append(rows, t3Row{kind: "t3.turn", key: "turn:" + thread.String + ":" + turn.String, occurred: *requestedAt, ended: completedAt, payload: map[string]any{
			"thread_id": thread.String, "turn_id": turn.String, "state": text(state),
			"requested_at": text(requested), "started_at": text(started), "completed_at": text(completed),
		}})
	}
	return rows, result.Err()
}

// readT3ThreadsV2 reads orchestration v2 threads into the same fields as
// readT3Threads. Thread ids carried over from state.sqlite are unchanged, so
// these are later revisions of the same records.
func readT3ThreadsV2(ctx context.Context, db *sql.DB) ([]t3Row, error) {
	result, err := db.QueryContext(ctx, `
		SELECT t.thread_id, t.project_id, t.title, t.created_at, t.updated_at, t.archived_at, t.deleted_at, t.runtime_mode, t.interaction_mode,
			t.payload_json,
			(SELECT max(m.created_at) FROM orchestration_v2_projection_messages m WHERE m.thread_id = t.thread_id AND m.role = 'user'),
			(SELECT count(*) FROM orchestration_v2_projection_runtime_requests r WHERE r.thread_id = t.thread_id AND r.status = 'pending'
				AND r.kind NOT IN ('user_input', 'dynamic_tool_call')),
			(SELECT count(*) FROM orchestration_v2_projection_runtime_requests r WHERE r.thread_id = t.thread_id AND r.status = 'pending'
				AND r.kind = 'user_input'),
			s.status, s.provider, s.updated_at,
			(SELECT r.run_id FROM orchestration_v2_projection_runs r WHERE r.thread_id = t.thread_id
				AND r.status IN ('preparing', 'queued', 'starting', 'running', 'waiting') ORDER BY r.ordinal DESC LIMIT 1)
		FROM orchestration_v2_projection_threads t
		LEFT JOIN orchestration_v2_projection_provider_sessions s ON s.provider_session_id = (
			SELECT provider_session_id FROM orchestration_v2_projection_provider_sessions WHERE thread_id = t.thread_id ORDER BY updated_at DESC LIMIT 1)`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []t3Row{}
	for result.Next() {
		var id, project, title, created, updated, archived, deleted, runtime, interaction, payloadJSON, latestUser, status, provider, sessionUpdated, activeRun sql.NullString
		var approvals, inputs sql.NullInt64
		if err := result.Scan(&id, &project, &title, &created, &updated, &archived, &deleted, &runtime, &interaction, &payloadJSON,
			&latestUser, &approvals, &inputs, &status, &provider, &sessionUpdated, &activeRun); err != nil {
			return nil, err
		}
		createdAt, err := parseT3Time(created)
		if err != nil || createdAt == nil {
			return nil, errors.Join(errors.New("thread without created_at"), err)
		}
		var payload struct {
			Branch    *string `json:"branch"`
			Worktree  *string `json:"worktreePath"`
			Selection struct {
				InstanceID string `json:"instanceId"`
				Model      string `json:"model"`
			} `json:"modelSelection"`
		}
		if err := json.Unmarshal([]byte(payloadJSON.String), &payload); err != nil {
			return nil, fmt.Errorf("thread %s payload: %w", id.String, err)
		}
		optional := func(value *string) any {
			if value == nil {
				return nil
			}
			return *value
		}
		rows = append(rows, t3Row{kind: "t3.thread", key: "thread:" + id.String, occurred: *createdAt, payload: map[string]any{
			"thread_id": id.String, "project_id": text(project), "title": text(title), "branch": optional(payload.Branch), "worktree_path": optional(payload.Worktree),
			"created_at": text(created), "updated_at": text(updated), "archived_at": text(archived), "deleted_at": text(deleted),
			"latest_user_message_at": text(latestUser), "pending_approval_count": approvals.Int64, "pending_user_input_count": inputs.Int64,
			"runtime_mode": text(runtime), "interaction_mode": text(interaction), "provider_instance": payload.Selection.InstanceID, "model": payload.Selection.Model,
			"session_status": text(status), "session_provider": text(provider), "active_turn_id": text(activeRun), "session_updated_at": text(sessionUpdated),
		}})
	}
	return rows, result.Err()
}

// t3RunStates maps an orchestration v2 run status onto the turn states of
// state.sqlite, which the server reads: an unfinished run is "running".
var t3RunStates = map[string]string{
	"preparing": "running", "queued": "running", "starting": "running", "running": "running", "waiting": "running",
	"completed": "completed", "interrupted": "interrupted", "cancelled": "interrupted", "rolled_back": "interrupted", "failed": "error",
}

// readT3RunsV2 reads orchestration v2 runs as turns: a run is what one
// message from the owner started.
func readT3RunsV2(ctx context.Context, db *sql.DB) ([]t3Row, error) {
	result, err := db.QueryContext(ctx, `SELECT thread_id, run_id, status, requested_at, json_extract(payload_json, '$.startedAt'), completed_at FROM orchestration_v2_projection_runs`)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []t3Row{}
	for result.Next() {
		var thread, run, status, requested, started, completed sql.NullString
		if err := result.Scan(&thread, &run, &status, &requested, &started, &completed); err != nil {
			return nil, err
		}
		state, known := t3RunStates[status.String]
		if !known {
			return nil, fmt.Errorf("run %s has unknown status %q", run.String, status.String)
		}
		requestedAt, err := parseT3Time(requested)
		if err != nil || requestedAt == nil {
			return nil, errors.Join(errors.New("run without requested_at"), err)
		}
		completedAt, err := parseT3Time(completed)
		if err != nil {
			return nil, err
		}
		rows = append(rows, t3Row{kind: "t3.turn", key: "turn:" + thread.String + ":" + run.String, occurred: *requestedAt, ended: completedAt, payload: map[string]any{
			"thread_id": thread.String, "turn_id": run.String, "state": state, "run_status": status.String,
			"requested_at": text(requested), "started_at": text(started), "completed_at": text(completed),
		}})
	}
	return rows, result.Err()
}
