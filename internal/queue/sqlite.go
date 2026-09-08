// Package queue provides the collector-side durable delivery queue.
package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	_ "modernc.org/sqlite"
)

var ErrLeaseNotHeld = errors.New("queue item is not held by this lease")

// Queue retains source observations until a central service acknowledges them.
// An acknowledged row is retained as a delivery audit record; it is never used
// as the authority for the source observation itself.
type Queue struct {
	db *sql.DB
}

type Item struct {
	ID          string
	Observation model.Observation
	Attempts    int
	LeaseToken  string
}

type Stats struct {
	Pending      int64
	Leased       int64
	Acknowledged int64
}

func Open(path string) (*Queue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create queue directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	q := &Queue{db: db}
	if err := q.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return q, nil
}

func (q *Queue) Close() error { return q.db.Close() }

func (q *Queue) migrate(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS queue_items (
			id TEXT PRIMARY KEY,
			source_instance_key TEXT NOT NULL,
			source_record_key TEXT NOT NULL,
			revision_digest TEXT NOT NULL,
			envelope_json BLOB NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('pending', 'leased', 'acknowledged')),
			attempts INTEGER NOT NULL DEFAULT 0,
			available_at TEXT NOT NULL,
			leased_until TEXT,
			lease_token TEXT,
			last_error TEXT,
			queued_at TEXT NOT NULL,
			acknowledged_at TEXT,
			UNIQUE (source_instance_key, source_record_key, revision_digest)
		);
		CREATE INDEX IF NOT EXISTS queue_items_claimable
			ON queue_items (state, available_at, queued_at);
		CREATE TABLE IF NOT EXISTS queue_state (
			state_key TEXT PRIMARY KEY,
			state_value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`)
	if err != nil {
		return fmt.Errorf("migrate queue: %w", err)
	}
	return nil
}

// Enqueue commits the captured observation before any network delivery attempt.
// Repeating the same logical record revision is harmless.
func (q *Queue) Enqueue(ctx context.Context, observation model.Observation, now time.Time) (bool, error) {
	if err := observation.Normalize(); err != nil {
		return false, err
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return false, fmt.Errorf("encode queue item: %w", err)
	}
	now = now.UTC()
	result, err := q.db.ExecContext(ctx, `
		INSERT INTO queue_items (
			id, source_instance_key, source_record_key, revision_digest, envelope_json,
			state, available_at, queued_at
		) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)
		ON CONFLICT (source_instance_key, source_record_key, revision_digest) DO NOTHING`,
		model.QueueItemID(observation), observation.SourceInstanceKey,
		observation.SourceRecordKey, observation.RevisionDigest, payload,
		timestamp(now), timestamp(now),
	)
	if err != nil {
		return false, fmt.Errorf("enqueue observation: %w", err)
	}
	inserted, err := result.RowsAffected()
	return inserted == 1, err
}

// Claim atomically leases at most limit delivery items. Expired leases can be
// reclaimed after a collector crash. Acknowledgement remains token-fenced.
func (q *Queue) Claim(ctx context.Context, limit int, leaseDuration time.Duration, now time.Time) ([]Item, error) {
	if limit <= 0 {
		return nil, errors.New("claim limit must be positive")
	}
	if leaseDuration <= 0 {
		return nil, errors.New("lease duration must be positive")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	until := now.Add(leaseDuration).UTC()
	rows, err := q.db.QueryContext(ctx, `
		UPDATE queue_items
		SET state = 'leased', lease_token = ?, leased_until = ?
		WHERE id IN (
			SELECT id FROM queue_items
			WHERE (state = 'pending' AND available_at <= ?)
			   OR (state = 'leased' AND leased_until <= ?)
			ORDER BY queued_at, id
			LIMIT ?
		)
		AND ((state = 'pending' AND available_at <= ?)
			OR (state = 'leased' AND leased_until <= ?))
		RETURNING id, envelope_json, attempts, lease_token`,
		token, timestamp(until), timestamp(now), timestamp(now), limit, timestamp(now), timestamp(now),
	)
	if err != nil {
		return nil, fmt.Errorf("claim queue items: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0, limit)
	for rows.Next() {
		var item Item
		var payload []byte
		if err := rows.Scan(&item.ID, &payload, &item.Attempts, &item.LeaseToken); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Observation); err != nil {
			return nil, fmt.Errorf("decode queued observation %s: %w", item.ID, err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Acknowledge marks a successfully persisted central receipt. It refuses a
// stale lease so one delivery worker cannot acknowledge another worker's claim.
func (q *Queue) Acknowledge(ctx context.Context, item Item, now time.Time) error {
	result, err := q.db.ExecContext(ctx, `
		UPDATE queue_items
		SET state = 'acknowledged', acknowledged_at = ?, lease_token = NULL, leased_until = NULL
		WHERE id = ? AND state = 'leased' AND lease_token = ?`,
		timestamp(now.UTC()), item.ID, item.LeaseToken,
	)
	if err != nil {
		return fmt.Errorf("acknowledge queue item: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrLeaseNotHeld
	}
	return nil
}

// Release returns a claimed item to the queue after a failed delivery. The
// caller chooses the next attempt time, keeping retry policy configurable.
func (q *Queue) Release(ctx context.Context, item Item, retryAt time.Time, cause error) error {
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	result, err := q.db.ExecContext(ctx, `
		UPDATE queue_items
		SET state = 'pending', attempts = attempts + 1, available_at = ?,
			lease_token = NULL, leased_until = NULL, last_error = ?
		WHERE id = ? AND state = 'leased' AND lease_token = ?`,
		timestamp(retryAt.UTC()), message, item.ID, item.LeaseToken,
	)
	if err != nil {
		return fmt.Errorf("release queue item: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrLeaseNotHeld
	}
	return nil
}

func (q *Queue) SaveCursor(ctx context.Context, key, value string, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO queue_state (state_key, state_value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (state_key) DO UPDATE SET state_value = excluded.state_value, updated_at = excluded.updated_at`,
		key, value, timestamp(now.UTC()),
	)
	return err
}

func (q *Queue) LoadCursor(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := q.db.QueryRowContext(ctx, `SELECT state_value FROM queue_state WHERE state_key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (q *Queue) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	rows, err := q.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM queue_items GROUP BY state`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return stats, err
		}
		switch state {
		case "pending":
			stats.Pending = count
		case "leased":
			stats.Leased = count
		case "acknowledged":
			stats.Acknowledged = count
		}
	}
	return stats, rows.Err()
}

func randomToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate lease token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
