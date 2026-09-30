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
type T3 struct {
	// Dir is T3's userdata directory, normally ~/.t3/userdata.
	Dir      string
	Interval time.Duration

	health Health
	// queued is the digest last queued per record, so unchanged rows are
	// skipped without touching the queue.
	queued  map[string]string
	mu      sync.Mutex
	counts  map[string]int
	checked time.Time
}

const t3SchemaVersion = "t3-state-projection-1"

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
	if !t.checked.IsZero() {
		status["rows"] = t.counts
	}
	return status
}

func (t *T3) collect(ctx context.Context, host Host, now time.Time) error {
	path := filepath.Join(t.Dir, "state.sqlite")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("T3 state database: %w", err)
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
	for _, read := range []func(context.Context, *sql.DB) ([]t3Row, error){readT3Projects, readT3Threads, readT3Turns} {
		found, err := read(ctx, db)
		if err != nil {
			return fmt.Errorf("read T3 state (its schema may have changed in an update): %w", err)
		}
		rows = append(rows, found...)
	}
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
			SourceSchemaVersion: t3SchemaVersion,
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
	t.counts, t.checked = counts, now
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
