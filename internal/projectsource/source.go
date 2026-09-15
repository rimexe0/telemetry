// Package projectsource reads the metadata and usage surfaces that T3 Code
// stores locally. It is deliberately read-only: T3 remains the owner of its
// state, while Telemetry receives bounded, revision-preserving observations.
package projectsource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
	_ "modernc.org/sqlite"
)

const (
	StateSchemaVersion = "t3code-state-sqlite-v49"
	UsageSchemaVersion = "t3code-usage-scan-cache-v3"
	GitSchemaVersion   = "git-status-porcelain-v1"
	DefaultOverlap     = 15 * time.Minute
	maxUsageCacheBytes = 64 << 20
	maxUsageRecords    = 200_000
	maxPassRecords     = 100_000
	maxSelectedThreads = 30_000
)

// Options selects the source files and projects for one bounded pass. A
// project path is always explicit; the adapter never walks the user's home or
// silently enables every project in the T3 database.
type Options struct {
	DeviceID       string
	DBPath         string
	UsageCachePath string
	ProjectPaths   []string
	From           time.Time
	Until          time.Time
	Overlap        time.Duration
	Limit          int
	Now            time.Time
	CollectGit     bool
	GitTimeout     time.Duration
}

type SourceResult struct {
	Observed        int      `json:"observed"`
	RevisionsQueued int      `json:"revisions_queued"`
	CursorAdvanced  bool     `json:"cursor_advanced"`
	Truncated       bool     `json:"truncated"`
	Status          string   `json:"status"`
	Warnings        []string `json:"warnings,omitempty"`
}

type SyncResult struct {
	State   SourceResult `json:"state"`
	Usage   SourceResult `json:"usage"`
	Git     SourceResult `json:"git"`
	Project []string     `json:"matched_projects,omitempty"`
}

type selectedProject struct {
	ID   string
	Path string
}

type cursorMode string

const (
	cursorWatermark cursorMode = "watermark"
	cursorPage      cursorMode = "page"
)

type recordCursor struct {
	At   string     `json:"at"`
	Key  string     `json:"key,omitempty"`
	Mode cursorMode `json:"mode"`
}

type stateCursor struct {
	Tables map[string]recordCursor `json:"tables"`
}

// ValidateQueuePaths rejects a queue path that aliases any selected source.
// queue.Open enables WAL and creates tables, so opening an aliased path would
// mutate the source even though the collector itself only issues read queries.
// Existing symlinks and hard links are checked by file identity.
func ValidateQueuePaths(queuePath string, sourcePaths ...string) error {
	if strings.TrimSpace(queuePath) == "" {
		return errors.New("queue path is required")
	}
	queuePath, err := filepath.Abs(filepath.Clean(queuePath))
	if err != nil {
		return fmt.Errorf("resolve queue path: %w", err)
	}
	queueInfo, queueErr := os.Stat(queuePath)
	if queueErr != nil && !errors.Is(queueErr, os.ErrNotExist) {
		return fmt.Errorf("stat queue path: %w", queueErr)
	}
	queueReal, queueRealErr := filepath.EvalSymlinks(queuePath)
	if queueRealErr != nil && !errors.Is(queueRealErr, os.ErrNotExist) {
		return fmt.Errorf("resolve queue symlinks: %w", queueRealErr)
	}
	for _, rawSourcePath := range sourcePaths {
		if strings.TrimSpace(rawSourcePath) == "" {
			continue
		}
		sourcePath, err := filepath.Abs(filepath.Clean(rawSourcePath))
		if err != nil {
			return fmt.Errorf("resolve source path: %w", err)
		}
		sourceInfo, sourceErr := os.Stat(sourcePath)
		if sourceErr != nil {
			return fmt.Errorf("stat source path: %w", sourceErr)
		}
		if queueErr == nil && os.SameFile(sourceInfo, queueInfo) {
			return errors.New("queue path aliases a selected source")
		}
		sourceReal, sourceRealErr := filepath.EvalSymlinks(sourcePath)
		if sourceRealErr != nil {
			return fmt.Errorf("resolve source symlinks: %w", sourceRealErr)
		}
		if queueRealErr == nil && filepath.Clean(sourceReal) == filepath.Clean(queueReal) {
			return errors.New("queue path aliases a selected source")
		}
	}
	return nil
}

// ValidateQueuePath is kept for callers that have one source path.
func ValidateQueuePath(sourceDBPath, queuePath string) error {
	if strings.TrimSpace(sourceDBPath) == "" {
		return errors.New("source database path is required")
	}
	path, err := filepath.Abs(filepath.Clean(sourceDBPath))
	if err != nil {
		return fmt.Errorf("resolve source database path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat source database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("source database is not a regular file")
	}
	return ValidateQueuePaths(queuePath, sourceDBPath)
}

// Sync reads the selected T3 state, optional T3 usage cache, and selected Git
// project status into the durable local Telemetry queue. It does not deliver
// anything to a central service. Each source has an independent keyset cursor,
// so a bounded usage backfill can make progress across repeated invocations.
func Sync(ctx context.Context, localQueue *queue.Queue, options Options) (SyncResult, error) {
	if localQueue == nil {
		return SyncResult{}, errors.New("queue is required")
	}
	if strings.TrimSpace(options.DeviceID) == "" {
		return SyncResult{}, errors.New("device id is required")
	}
	if strings.TrimSpace(options.DBPath) == "" {
		return SyncResult{}, errors.New("T3 Code state database path is required")
	}
	if len(options.ProjectPaths) == 0 {
		return SyncResult{}, errors.New("at least one explicit project path is required")
	}
	if options.Limit <= 0 {
		return SyncResult{}, errors.New("limit must be positive")
	}
	if options.Limit > maxPassRecords {
		return SyncResult{}, fmt.Errorf("limit cannot exceed %d", maxPassRecords)
	}
	if options.Overlap < 0 {
		return SyncResult{}, errors.New("overlap cannot be negative")
	}
	if options.Overlap == 0 {
		options.Overlap = DefaultOverlap
	}
	if options.GitTimeout == 0 {
		options.GitTimeout = 5 * time.Second
	}
	if options.GitTimeout < 0 {
		return SyncResult{}, errors.New("git timeout cannot be negative")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	if options.Until.IsZero() {
		options.Until = options.Now
	} else {
		options.Until = options.Until.UTC()
	}
	if !options.From.IsZero() && !options.Until.After(options.From.UTC()) {
		return SyncResult{}, errors.New("until must be after from")
	}

	paths := make([]string, 0, len(options.ProjectPaths))
	seenPaths := make(map[string]struct{}, len(options.ProjectPaths))
	for _, raw := range options.ProjectPaths {
		if strings.TrimSpace(raw) == "" {
			return SyncResult{}, errors.New("project path cannot be empty or whitespace")
		}
		if strings.TrimSpace(raw) != raw {
			return SyncResult{}, errors.New("project path cannot have leading or trailing whitespace")
		}
		path, err := filepath.Abs(filepath.Clean(raw))
		if err != nil {
			return SyncResult{}, fmt.Errorf("resolve project path %q: %w", raw, err)
		}
		if _, seen := seenPaths[path]; seen {
			continue
		}
		seenPaths[path] = struct{}{}
		paths = append(paths, path)
	}

	stateResult, matched, err := syncState(ctx, localQueue, options, paths)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{State: stateResult}
	for _, project := range matched {
		result.Project = append(result.Project, project.Path)
	}

	if options.UsageCachePath != "" {
		usageResult, err := syncUsage(ctx, localQueue, options)
		if err != nil {
			return result, err
		}
		result.Usage = usageResult
	} else {
		result.Usage.Status = "not_selected"
		result.Usage.Warnings = []string{"usage cache was not selected; no provider token counts were read"}
	}

	if options.CollectGit {
		gitResult := syncGit(ctx, localQueue, options, paths)
		result.Git = gitResult
	} else {
		result.Git.Status = "not_selected"
	}
	return result, nil
}

func syncState(ctx context.Context, localQueue *queue.Queue, options Options, paths []string) (SourceResult, []selectedProject, error) {
	db, err := openReadOnly(options.DBPath)
	if err != nil {
		return SourceResult{}, nil, err
	}
	defer db.Close()

	matched, err := selectProjects(ctx, db, paths)
	if err != nil {
		return SourceResult{}, nil, err
	}
	result := SourceResult{Status: "ok"}
	stateKey := cursorKey("state", options.DeviceID, paths)
	cursor, found, err := loadStateCursor(ctx, localQueue, stateKey)
	if err != nil {
		return result, matched, err
	}
	if !found && options.From.IsZero() {
		return result, matched, errors.New("initial T3 Code sync requires an explicit from time")
	}

	// Project rows are selected snapshots. They are small and are read on every
	// pass so a newly selected project is visible even if it was created before
	// the incremental cursor.
	for _, project := range matched {
		rows, queryErr := db.QueryContext(ctx, `
			SELECT project_id, title, workspace_root, created_at, updated_at, deleted_at
			FROM projection_projects WHERE project_id = ?`, project.ID)
		if queryErr != nil {
			return result, matched, fmt.Errorf("read T3 project %s: %w", project.ID, queryErr)
		}
		for rows.Next() {
			var id, title, root, created, updated string
			var deleted sql.NullString
			if err := rows.Scan(&id, &title, &root, &created, &updated, &deleted); err != nil {
				rows.Close()
				return result, matched, fmt.Errorf("scan T3 project: %w", err)
			}
			occurred, err := parseSourceTime(updated)
			if err != nil {
				rows.Close()
				return result, matched, fmt.Errorf("project %s updated_at: %w", id, err)
			}
			payload := map[string]any{
				"project_id": id, "title": title, "workspace_root": root,
				"created_at": created, "updated_at": updated,
				"deleted_at": nullableString(deleted),
			}
			observation, err := makeObservation(options.DeviceID, "t3code:"+options.DeviceID+":state", "project:"+id, "t3code.project", occurred, nil, StateSchemaVersion, payload)
			if err != nil {
				rows.Close()
				return result, matched, err
			}
			if err := enqueue(ctx, localQueue, &result, observation, options.Now); err != nil {
				rows.Close()
				return result, matched, err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return result, matched, err
		}
		rows.Close()
	}

	projectIDs := make([]string, 0, len(matched))
	for _, project := range matched {
		projectIDs = append(projectIDs, project.ID)
	}
	threadIDs, err := selectThreadIDs(ctx, db, projectIDs)
	if err != nil {
		return result, matched, err
	}
	for _, table := range []string{"threads", "turns", "thread_sessions", "provider_runtime"} {
		var observations []model.Observation
		var more bool
		var last recordCursor
		start, key, err := cursorStart(cursor.Tables[table], options.From, options.Until, options.Overlap)
		if err != nil {
			return result, matched, fmt.Errorf("%s cursor: %w", table, err)
		}
		switch table {
		case "threads":
			observations, more, last, err = readThreads(ctx, db, options, projectIDs, start, key)
		case "turns":
			observations, more, last, err = readTurns(ctx, db, options, threadIDs, start, key)
		case "thread_sessions":
			observations, more, last, err = readThreadSessions(ctx, db, options, threadIDs, start, key)
		case "provider_runtime":
			observations, more, last, err = readProviderRuntime(ctx, db, options, threadIDs, start, key)
		}
		if err != nil {
			return result, matched, err
		}
		for _, observation := range observations {
			if err := enqueue(ctx, localQueue, &result, observation, options.Now); err != nil {
				return result, matched, err
			}
		}
		if more {
			result.Truncated = true
			cursor.Tables[table] = last
		} else {
			cursor.Tables[table] = recordCursor{At: options.Until.Format(time.RFC3339Nano), Mode: cursorWatermark}
		}
	}
	if len(matched) == 0 {
		result.Status = "partial"
		result.Warnings = append(result.Warnings, "no T3 project row matched the selected path; Git status may still be collected")
	}
	if err := saveStateCursor(ctx, localQueue, stateKey, cursor, options.Now); err != nil {
		return result, matched, err
	}
	result.CursorAdvanced = true
	return result, matched, nil
}

func selectThreadIDs(ctx context.Context, db *sql.DB, projectIDs []string) ([]string, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(projectIDs)), ",")
	args := make([]any, 0, len(projectIDs)+1)
	for _, id := range projectIDs {
		args = append(args, id)
	}
	args = append(args, maxSelectedThreads+1)
	rows, err := db.QueryContext(ctx, `SELECT thread_id FROM projection_threads WHERE project_id IN (`+placeholders+`) ORDER BY thread_id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("find T3 threads for selected projects: %w", err)
	}
	defer rows.Close()
	threadIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		threadIDs = append(threadIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(threadIDs) > maxSelectedThreads {
		return nil, fmt.Errorf("selected T3 projects contain more than %d threads; split the selection before collecting", maxSelectedThreads)
	}
	return threadIDs, nil
}

func selectProjects(ctx context.Context, db *sql.DB, paths []string) ([]selectedProject, error) {
	matched := make([]selectedProject, 0, len(paths))
	for _, path := range paths {
		rows, err := db.QueryContext(ctx, `SELECT project_id, workspace_root FROM projection_projects WHERE workspace_root = ?`, path)
		if err != nil {
			return nil, fmt.Errorf("find T3 project %q: %w", path, err)
		}
		for rows.Next() {
			var projectID, root string
			if err := rows.Scan(&projectID, &root); err != nil {
				rows.Close()
				return nil, err
			}
			matched = append(matched, selectedProject{ID: projectID, Path: root})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return matched, nil
}

func readThreads(ctx context.Context, db *sql.DB, options Options, projectIDs []string, start time.Time, key string) ([]model.Observation, bool, recordCursor, error) {
	if len(projectIDs) == 0 {
		return nil, false, recordCursor{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(projectIDs)), ",")
	args := make([]any, 0, len(projectIDs)+4)
	for _, id := range projectIDs {
		args = append(args, id)
	}
	args = append(args, start.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), strings.TrimPrefix(key, "thread:"), options.Until.Format(time.RFC3339Nano), options.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT thread_id, project_id, branch, worktree_path, created_at, updated_at, deleted_at, runtime_mode, interaction_mode
		FROM projection_threads WHERE project_id IN (`+placeholders+`) AND (updated_at > ? OR (updated_at = ? AND thread_id > ?)) AND updated_at < ? ORDER BY updated_at, thread_id LIMIT ?`, args...)
	if err != nil {
		return nil, false, recordCursor{}, fmt.Errorf("read T3 threads: %w", err)
	}
	defer rows.Close()
	observations := make([]model.Observation, 0, options.Limit)
	for rows.Next() {
		var id, projectID, created, updated, runtimeMode, interactionMode string
		var branch, worktree, deleted sql.NullString
		if err := rows.Scan(&id, &projectID, &branch, &worktree, &created, &updated, &deleted, &runtimeMode, &interactionMode); err != nil {
			return nil, false, recordCursor{}, err
		}
		at, err := parseSourceTime(updated)
		if err != nil {
			return nil, false, recordCursor{}, fmt.Errorf("thread %s updated_at: %w", id, err)
		}
		if !afterCursor(at, "thread:"+id, start, key) {
			continue
		}
		if !at.Before(options.Until) {
			continue
		}
		payload := map[string]any{"thread_id": id, "project_id": projectID, "branch": nullableString(branch), "worktree_path": nullableString(worktree), "created_at": created, "updated_at": updated, "deleted_at": nullableString(deleted), "runtime_mode": runtimeMode, "interaction_mode": interactionMode}
		observation, err := makeObservation(options.DeviceID, "t3code:"+options.DeviceID+":state", "thread:"+id, "t3code.thread", at, nil, StateSchemaVersion, payload)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		observations = append(observations, observation)
		if len(observations) > options.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, recordCursor{}, err
	}
	more := len(observations) > options.Limit
	if more {
		observations = observations[:options.Limit]
		return observations, true, cursorForObservation(observations[len(observations)-1], cursorPage), nil
	}
	return observations, false, recordCursor{}, nil
}

func readTurns(ctx context.Context, db *sql.DB, options Options, threadIDs []string, start time.Time, key string) ([]model.Observation, bool, recordCursor, error) {
	if len(threadIDs) == 0 {
		return nil, false, recordCursor{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(threadIDs)), ",")
	args := make([]any, 0, len(threadIDs)+6)
	for _, id := range threadIDs {
		args = append(args, id)
	}
	cursorThread, cursorTurn := turnCursorParts(key)
	lowerBound := `(COALESCE(completed_at, started_at, requested_at) > ? OR (COALESCE(completed_at, started_at, requested_at) = ? AND (thread_id > ? OR (thread_id = ? AND COALESCE('id:' || turn_id, 'row:' || CAST(row_id AS TEXT)) > ?))))`
	if key == "" {
		lowerBound = `(completed_at IS NULL OR ` + lowerBound + `)`
	}
	args = append(args, options.Until.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), cursorThread, cursorThread, cursorTurn, options.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT row_id, thread_id, turn_id, state, requested_at, started_at, completed_at, checkpoint_status
		FROM projection_turns WHERE thread_id IN (`+placeholders+`) AND COALESCE(completed_at, started_at, requested_at) < ? AND `+lowerBound+` ORDER BY COALESCE(completed_at, started_at, requested_at), thread_id, COALESCE('id:' || turn_id, 'row:' || CAST(row_id AS TEXT)) LIMIT ?`, args...)
	if err != nil {
		return nil, false, recordCursor{}, fmt.Errorf("read T3 turns: %w", err)
	}
	defer rows.Close()
	observations := make([]model.Observation, 0, options.Limit)
	for rows.Next() {
		var rowID int64
		var threadID, state, requested string
		var turnID sql.NullString
		var started, completed, checkpoint sql.NullString
		if err := rows.Scan(&rowID, &threadID, &turnID, &state, &requested, &started, &completed, &checkpoint); err != nil {
			return nil, false, recordCursor{}, err
		}
		at, err := parseSourceTime(firstNonNull(completed, started, sql.NullString{String: requested, Valid: true}))
		if err != nil {
			return nil, false, recordCursor{}, fmt.Errorf("turn %d time: %w", rowID, err)
		}
		turnKey := "row:" + fmt.Sprint(rowID)
		if turnID.Valid && turnID.String != "" {
			turnKey = "id:" + turnID.String
		}
		obsKey := "turn:" + threadID + ":" + turnKey
		if completed.Valid && !afterCursor(at, obsKey, start, key) {
			continue
		}
		if !completed.Valid && key != "" && !afterCursor(at, obsKey, start, key) {
			continue
		}
		if !at.Before(options.Until) {
			continue
		}
		var ended *time.Time
		if completed.Valid {
			value, parseErr := parseSourceTime(completed.String)
			if parseErr != nil {
				return nil, false, recordCursor{}, parseErr
			}
			ended = &value
		}
		payload := map[string]any{"row_id": rowID, "thread_id": threadID, "turn_id": nullableString(turnID), "state": state, "requested_at": requested, "started_at": nullableString(started), "completed_at": nullableString(completed), "checkpoint_status": nullableString(checkpoint)}
		observation, err := makeObservation(options.DeviceID, "t3code:"+options.DeviceID+":state", obsKey, "t3code.turn", at, ended, StateSchemaVersion, payload)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		observations = append(observations, observation)
		if len(observations) > options.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, recordCursor{}, err
	}
	if len(observations) > options.Limit {
		observations = observations[:options.Limit]
		return observations, true, cursorForObservation(observations[len(observations)-1], cursorPage), nil
	}
	return observations, false, recordCursor{}, nil
}

func readThreadSessions(ctx context.Context, db *sql.DB, options Options, threadIDs []string, start time.Time, key string) ([]model.Observation, bool, recordCursor, error) {
	if len(threadIDs) == 0 {
		return nil, false, recordCursor{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(threadIDs)), ",")
	args := make([]any, 0, len(threadIDs)+3)
	for _, id := range threadIDs {
		args = append(args, id)
	}
	args = append(args, start.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), strings.TrimPrefix(key, "session:"), options.Until.Format(time.RFC3339Nano), options.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT thread_id, status, provider_name, runtime_mode, updated_at FROM projection_thread_sessions WHERE thread_id IN (`+placeholders+`) AND (updated_at > ? OR (updated_at = ? AND thread_id > ?)) AND updated_at < ? ORDER BY updated_at, thread_id LIMIT ?`, args...)
	if err != nil {
		return nil, false, recordCursor{}, fmt.Errorf("read T3 sessions: %w", err)
	}
	defer rows.Close()
	observations := make([]model.Observation, 0, options.Limit)
	for rows.Next() {
		var threadID, status, runtimeMode, updated string
		var provider sql.NullString
		if err := rows.Scan(&threadID, &status, &provider, &runtimeMode, &updated); err != nil {
			return nil, false, recordCursor{}, err
		}
		at, err := parseSourceTime(updated)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		obsKey := "session:" + threadID
		if !afterCursor(at, obsKey, start, key) || !at.Before(options.Until) {
			continue
		}
		payload := map[string]any{"thread_id": threadID, "status": status, "provider_name": nullableString(provider), "runtime_mode": runtimeMode, "updated_at": updated}
		observation, err := makeObservation(options.DeviceID, "t3code:"+options.DeviceID+":state", obsKey, "t3code.thread_session", at, nil, StateSchemaVersion, payload)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		observations = append(observations, observation)
		if len(observations) > options.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, recordCursor{}, err
	}
	if len(observations) > options.Limit {
		observations = observations[:options.Limit]
		return observations, true, cursorForObservation(observations[len(observations)-1], cursorPage), nil
	}
	return observations, false, recordCursor{}, nil
}

func readProviderRuntime(ctx context.Context, db *sql.DB, options Options, threadIDs []string, start time.Time, key string) ([]model.Observation, bool, recordCursor, error) {
	if len(threadIDs) == 0 {
		return nil, false, recordCursor{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(threadIDs)), ",")
	args := make([]any, 0, len(threadIDs)+3)
	for _, id := range threadIDs {
		args = append(args, id)
	}
	args = append(args, start.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano), strings.TrimPrefix(key, "provider-runtime:"), options.Until.Format(time.RFC3339Nano), options.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT thread_id, provider_name, adapter_key, runtime_mode, status, last_seen_at, provider_instance_id, runtime_payload_json FROM provider_session_runtime WHERE thread_id IN (`+placeholders+`) AND (last_seen_at > ? OR (last_seen_at = ? AND thread_id > ?)) AND last_seen_at < ? ORDER BY last_seen_at, thread_id LIMIT ?`, args...)
	if err != nil {
		return nil, false, recordCursor{}, fmt.Errorf("read T3 provider runtime: %w", err)
	}
	defer rows.Close()
	observations := make([]model.Observation, 0, options.Limit)
	for rows.Next() {
		var threadID, provider, adapter, runtimeMode, status, lastSeen string
		var instance, runtimePayload sql.NullString
		if err := rows.Scan(&threadID, &provider, &adapter, &runtimeMode, &status, &lastSeen, &instance, &runtimePayload); err != nil {
			return nil, false, recordCursor{}, err
		}
		at, err := parseSourceTime(lastSeen)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		obsKey := "provider-runtime:" + threadID
		if !afterCursor(at, obsKey, start, key) || !at.Before(options.Until) {
			continue
		}
		payload := map[string]any{"thread_id": threadID, "provider_name": provider, "adapter_key": adapter, "runtime_mode": runtimeMode, "status": status, "last_seen_at": lastSeen, "provider_instance_id": nullableString(instance), "model": runtimeModel(runtimePayload)}
		observation, err := makeObservation(options.DeviceID, "t3code:"+options.DeviceID+":state", obsKey, "t3code.provider_runtime", at, nil, StateSchemaVersion, payload)
		if err != nil {
			return nil, false, recordCursor{}, err
		}
		observations = append(observations, observation)
		if len(observations) > options.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, recordCursor{}, err
	}
	if len(observations) > options.Limit {
		observations = observations[:options.Limit]
		return observations, true, cursorForObservation(observations[len(observations)-1], cursorPage), nil
	}
	return observations, false, recordCursor{}, nil
}

type usageCache struct {
	Version  int                       `json:"version"`
	Models   []string                  `json:"models"`
	Sessions []string                  `json:"sessions"`
	Files    map[string]usageCacheFile `json:"files"`
}

type usageCacheFile struct {
	Provider string  `json:"p"`
	Records  [][]any `json:"r"`
	Tail     [][]any `json:"t"`
}

type usageItem struct {
	At       time.Time
	Key      string
	Provider string
	Model    string
	Session  string
	RowIndex int
	Row      []any
}

func syncUsage(ctx context.Context, localQueue *queue.Queue, options Options) (SourceResult, error) {
	result := SourceResult{Status: "ok"}
	data, err := os.ReadFile(options.UsageCachePath)
	if err != nil {
		return result, fmt.Errorf("read T3 usage cache: %w", err)
	}
	if len(data) > maxUsageCacheBytes {
		return result, fmt.Errorf("T3 usage cache exceeds bounded read size of %d bytes", maxUsageCacheBytes)
	}
	var cache usageCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return result, fmt.Errorf("decode T3 usage cache: %w", err)
	}
	if cache.Version != 3 {
		return result, fmt.Errorf("unsupported T3 usage cache version %d", cache.Version)
	}
	if cache.Models == nil || cache.Sessions == nil || cache.Files == nil {
		return result, errors.New("T3 usage cache requires non-null models, sessions, and files")
	}
	items := make([]usageItem, 0)
	malformed := false
	for filePath, file := range cache.Files {
		if strings.TrimSpace(filePath) == "" || strings.TrimSpace(file.Provider) == "" || file.Records == nil || file.Tail == nil {
			return result, fmt.Errorf("T3 usage cache file %q has an invalid provider or record arrays", filePath)
		}
		for rowIndex, row := range file.Records {
			item, ok := parseUsageItem(filePath, file.Provider, row, cache.Models, cache.Sessions, rowIndex)
			if !ok {
				malformed = true
				result.Status = "partial"
				result.Warnings = append(result.Warnings, "skipped malformed usage cache row")
				continue
			}
			items = append(items, item)
			if len(items) > maxUsageRecords {
				return result, fmt.Errorf("T3 usage cache exceeds bounded record size of %d rows", maxUsageRecords)
			}
		}
		for rowIndex, row := range file.Tail {
			item, ok := parseUsageItem(filePath, file.Provider, row, cache.Models, cache.Sessions, len(file.Records)+rowIndex)
			if !ok {
				malformed = true
				result.Status = "partial"
				result.Warnings = append(result.Warnings, "skipped malformed usage cache row")
				continue
			}
			items = append(items, item)
			if len(items) > maxUsageRecords {
				return result, fmt.Errorf("T3 usage cache exceeds bounded record size of %d rows", maxUsageRecords)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].At.Equal(items[j].At) {
			return items[i].Key < items[j].Key
		}
		return items[i].At.Before(items[j].At)
	})
	stateKey := cursorKey("usage", options.DeviceID, []string{options.UsageCachePath})
	state, found, err := loadStateCursor(ctx, localQueue, stateKey)
	if err != nil {
		return result, err
	}
	c, foundCursor := state.Tables["usage"]
	if !found && !foundCursor && options.From.IsZero() {
		return result, errors.New("initial T3 usage sync requires an explicit from time")
	}
	start, key, err := cursorStart(c, options.From, options.Until, options.Overlap)
	if err != nil {
		return result, err
	}
	selected := make([]model.Observation, 0, options.Limit)
	more := false
	for _, item := range items {
		if !afterCursor(item.At, item.Key, start, key) || !item.At.Before(options.Until) {
			continue
		}
		if len(selected) == options.Limit {
			more = true
			break
		}
		observation, err := usageObservation(options.DeviceID, options.UsageCachePath, item)
		if err != nil {
			return result, err
		}
		selected = append(selected, observation)
	}
	for _, observation := range selected {
		if err := enqueue(ctx, localQueue, &result, observation, options.Now); err != nil {
			return result, err
		}
	}
	if malformed {
		// Keep the existing cursor so repairing the cache cannot strand rows
		// behind a watermark that was advanced past this malformed input.
		return result, nil
	}
	if more {
		result.Truncated = true
		state.Tables["usage"] = cursorForObservation(selected[len(selected)-1], cursorPage)
	} else {
		state.Tables["usage"] = recordCursor{At: options.Until.Format(time.RFC3339Nano), Mode: cursorWatermark}
	}
	if err := saveStateCursor(ctx, localQueue, stateKey, state, options.Now); err != nil {
		return result, err
	}
	result.CursorAdvanced = true
	return result, nil
}

func parseUsageItem(filePath, provider string, row []any, models, sessions []string, rowIndex int) (usageItem, bool) {
	if len(row) < 10 || provider == "" {
		return usageItem{}, false
	}
	timestamp, ok := number(row[0])
	if !ok || timestamp <= 0 {
		return usageItem{}, false
	}
	modelIndex, ok := number(row[1])
	if !ok || int(modelIndex) < 0 || int(modelIndex) >= len(models) {
		return usageItem{}, false
	}
	sessionIndex, ok := number(row[2])
	if !ok || int(sessionIndex) < 0 || int(sessionIndex) >= len(sessions) {
		return usageItem{}, false
	}
	for _, value := range row[3:8] {
		n, valid := number(value)
		if !valid || n < 0 {
			return usageItem{}, false
		}
	}
	modelName := models[int(modelIndex)]
	session := sessions[int(sessionIndex)]
	identity := fmt.Sprintf("%s|%s|row:%d", provider, filePath, rowIndex)
	// A null cache dedupe key means the provider declared this record unique.
	// The cache is append-only, so the file-relative row index is the stable
	// fallback; using only file/model would collapse an entire rollout into one
	// logical observation with many misleading revisions.
	if dedupe, ok := row[8].(string); ok && dedupe != "" {
		identity = provider + "|dedupe|" + dedupe
	}
	digest := sha256.Sum256([]byte(identity))
	key := "usage:" + hex.EncodeToString(digest[:])
	return usageItem{At: time.UnixMilli(int64(timestamp)).UTC(), Key: key, Provider: provider, Model: modelName, Session: session, RowIndex: rowIndex, Row: append([]any(nil), row...)}, true
}

func usageObservation(deviceID, cachePath string, item usageItem) (model.Observation, error) {
	sessionDigest := sha256.Sum256([]byte(item.Session))
	rowDigest := sha256.Sum256([]byte(formatJSONValue(item.Row)))
	payload := map[string]any{
		"provider": item.Provider, "model": item.Model,
		"session_id_hash": hex.EncodeToString(sessionDigest[:]),
		"cache_row_index": item.RowIndex, "cache_row": item.Row, "row_digest": hex.EncodeToString(rowDigest[:]),
		"usage_cache_path_hash": digestString(cachePath),
	}
	return makeObservation(deviceID, "t3code:"+deviceID+":usage-cache", item.Key, "t3code.provider_usage", item.At, nil, UsageSchemaVersion, payload)
}

func syncGit(ctx context.Context, localQueue *queue.Queue, options Options, paths []string) SourceResult {
	result := SourceResult{Status: "ok"}
	for _, path := range paths {
		gitContext, cancel := context.WithTimeout(ctx, options.GitTimeout)
		status, err := readGitStatus(gitContext, path)
		cancel()
		if err != nil {
			result.Status = "partial"
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		payload := map[string]any{"project_path": status.ProjectRoot, "branch": status.Branch, "head": status.Head, "staged": status.Staged, "unstaged": status.Unstaged, "untracked": status.Untracked, "conflicted": status.Conflicted}
		key := "status:" + digestString(status.ProjectRoot)
		observation, err := makeObservation(options.DeviceID, "git:"+options.DeviceID, key, "git.project_status", options.Now, nil, GitSchemaVersion, payload)
		if err != nil {
			result.Status = "partial"
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		if err := enqueue(ctx, localQueue, &result, observation, options.Now); err != nil {
			result.Status = "partial"
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
	}
	result.CursorAdvanced = result.Status == "ok"
	return result
}

type gitStatus struct {
	ProjectRoot, Branch, Head               string
	Staged, Unstaged, Untracked, Conflicted int
}

var errGitOutputLimit = errors.New("git status output exceeded 1 MiB")

func readGitStatus(ctx context.Context, path string) (gitStatus, error) {
	root, err := runGit(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitStatus{}, err
	}
	branch, err := runGit(ctx, path, "branch", "--show-current")
	if err != nil {
		return gitStatus{}, err
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return gitStatus{}, err
	}
	porcelain, err := runGit(ctx, path, "status", "--porcelain=v1", "--branch", "--untracked-files=normal")
	if err != nil {
		return gitStatus{}, err
	}
	status := parseGitStatus(strings.TrimSpace(root), strings.TrimSpace(branch), strings.TrimSpace(head), porcelain)
	return status, nil
}

func parseGitStatus(root, branch, head, porcelain string) gitStatus {
	status := gitStatus{ProjectRoot: root, Branch: branch, Head: head}
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.HasPrefix(line, "## ") || len(line) < 2 {
			continue
		}
		x, y := line[0], line[1]
		if x == '?' && y == '?' {
			status.Untracked++
			continue
		}
		if x == 'U' || y == 'U' || (x == 'A' && y == 'A') {
			status.Conflicted++
		}
		if x != ' ' {
			status.Staged++
		}
		if y != ' ' {
			status.Unstaged++
		}
	}
	return status
}

func runGit(ctx context.Context, path string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", path}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = withGitOptionalLocksDisabled(os.Environ())
	var output limitedBuffer
	var stderr limitedBuffer
	command.Stdout = &output
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(output.String())
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return output.String(), nil
}

func withGitOptionalLocksDisabled(environment []string) []string {
	result := append([]string(nil), environment...)
	for index, value := range result {
		if strings.HasPrefix(value, "GIT_OPTIONAL_LOCKS=") {
			result[index] = "GIT_OPTIONAL_LOCKS=0"
			return result
		}
	}
	return append(result, "GIT_OPTIONAL_LOCKS=0")
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errGitOutputLimit
	}
	return b.Buffer.Write(p)
}

func makeObservation(deviceID, instance, key, kind string, occurred time.Time, ended *time.Time, schema string, payload any) (model.Observation, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return model.Observation{}, fmt.Errorf("encode %s: %w", kind, err)
	}
	observation := model.Observation{SourceInstanceKey: instance, SourceKind: "t3code", DeviceID: deviceID, SourceRecordKey: key, Kind: kind, OccurredAt: occurred.UTC(), CollectedAt: time.Now().UTC(), EndedAt: ended, SourceSchemaVersion: schema, OriginalPayload: raw}
	if strings.HasPrefix(kind, "git.") {
		observation.SourceKind = "git"
	}
	if err := observation.Normalize(); err != nil {
		return model.Observation{}, fmt.Errorf("normalize %s: %w", kind, err)
	}
	return observation, nil
}

func enqueue(ctx context.Context, localQueue *queue.Queue, result *SourceResult, observation model.Observation, now time.Time) error {
	inserted, err := localQueue.Enqueue(ctx, observation, now)
	if err != nil {
		return err
	}
	result.Observed++
	if inserted {
		result.RevisionsQueued++
	}
	return nil
}

func cursorKey(kind, device string, paths []string) string {
	return "t3code.watermark.v1." + kind + "." + device + "." + digestString(strings.Join(paths, "\x00"))
}

func loadStateCursor(ctx context.Context, q *queue.Queue, key string) (stateCursor, bool, error) {
	value, found, err := q.LoadCursor(ctx, key)
	if err != nil {
		return stateCursor{}, false, err
	}
	if !found {
		return stateCursor{Tables: make(map[string]recordCursor)}, false, nil
	}
	var state stateCursor
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return stateCursor{}, false, fmt.Errorf("decode cursor %s: %w", key, err)
	}
	if state.Tables == nil {
		state.Tables = make(map[string]recordCursor)
	}
	return state, true, nil
}

func saveStateCursor(ctx context.Context, q *queue.Queue, key string, state stateCursor, now time.Time) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return q.SaveCursor(ctx, key, string(data), now)
}

func cursorStart(c recordCursor, explicit, until time.Time, overlap time.Duration) (time.Time, string, error) {
	if c.At == "" {
		return explicit.UTC(), "", nil
	}
	at, err := parseSourceTime(c.At)
	if err != nil {
		return time.Time{}, "", err
	}
	if c.Mode == cursorPage {
		return at, c.Key, nil
	}
	return at.Add(-overlap), "", nil
}

func cursorForObservation(observation model.Observation, mode cursorMode) recordCursor {
	return recordCursor{At: observation.OccurredAt.UTC().Format(time.RFC3339Nano), Key: observation.SourceRecordKey, Mode: mode}
}

func turnCursorParts(key string) (string, string) {
	rest := strings.TrimPrefix(key, "turn:")
	if rest == key {
		return "", ""
	}
	threadID, turnKey, found := strings.Cut(rest, ":")
	if !found {
		return "", ""
	}
	return threadID, turnKey
}

func afterCursor(at time.Time, key string, start time.Time, cursorKey string) bool {
	return at.After(start) || (at.Equal(start) && key > cursorKey)
}

func parseSourceTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00"} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.UTC(), nil
			}
		}
		return time.Time{}, fmt.Errorf("invalid source time %q", typed)
	case time.Time:
		return typed.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("invalid source time type %T", value)
	}
}

func nullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func runtimeModel(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(raw.String), &payload); err != nil || payload.Model == "" {
		return nil
	}
	return payload.Model
}

func firstNonNull(values ...sql.NullString) string {
	for _, value := range values {
		if value.Valid && value.String != "" {
			return value.String
		}
	}
	return ""
}
func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
func formatJSONValue(value any) string { data, _ := json.Marshal(value); return string(data) }
func digestString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func openReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(absolute); err != nil {
		return nil, fmt.Errorf("stat T3 Code state database: %w", err)
	} else if !info.Mode().IsRegular() {
		return nil, errors.New("T3 Code state database is not a regular file")
	}
	db, err := sql.Open("sqlite", "file:"+absolute+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open T3 Code state database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping T3 Code state database: %w", err)
	}
	return db, nil
}
