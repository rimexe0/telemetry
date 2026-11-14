package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rimexe0/telemetry/internal/intent"
)

// Intent is one thing the system thinks the owner is trying to do. It is an
// inference from the records, never something the owner said.
type Intent struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	NextStep   string    `json:"next_step,omitempty"`
	Evidence   []string  `json:"evidence"`
	Confidence string    `json:"confidence,omitempty"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	ReadingID  int64     `json:"reading_id"`
}

// IntentReading is one reading, kept as it was made.
type IntentReading struct {
	ID          int64     `json:"id"`
	ReadAt      time.Time `json:"read_at"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Note        string    `json:"note,omitempty"`
	NowIntentID *int64    `json:"now_intent_id,omitempty"`
}

// SaveIntentReading keeps a reading of [from, to) and applies it to the
// running list: aims with an earlier id change that intent, new aims become
// intents, and an intent still active or waiting that this reading did not
// see during the window is paused.
func (p *Postgres) SaveIntentReading(ctx context.Context, from, to time.Time, digest, output string, reading intent.Reading) (IntentReading, error) {
	saved := IntentReading{From: from, To: to, Note: reading.Note}
	answer, err := json.Marshal(reading)
	if err != nil {
		return saved, err
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return saved, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO intent_readings (window_from, window_to, digest, output, answer, note) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, read_at`, from, to, digest, output, answer, reading.Note).Scan(&saved.ID, &saved.ReadAt); err != nil {
		return saved, err
	}
	touched := []int64{}
	for position, aim := range reading.Aims {
		evidence, _ := json.Marshal(nonNil(aim.Evidence))
		seen := aim.Status == intent.StatusActive || aim.Status == intent.StatusWaiting
		var id int64
		if aim.ID != nil {
			err = tx.QueryRow(ctx, `UPDATE intents SET title = $2, kind = $3, status = $4, next_step = $5, evidence = $6, confidence = $7,
				last_seen = CASE WHEN $8 THEN $9 ELSE last_seen END, reading_id = $10 WHERE id = $1 RETURNING id`,
				*aim.ID, aim.Title, aim.Kind, aim.Status, aim.NextStep, evidence, aim.Confidence, seen, to, saved.ID).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				err, aim.ID = nil, nil
			}
			if err != nil {
				return saved, err
			}
		}
		if aim.ID == nil {
			if err := tx.QueryRow(ctx, `INSERT INTO intents (title, kind, status, next_step, evidence, confidence, first_seen, last_seen, reading_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8) RETURNING id`,
				aim.Title, aim.Kind, aim.Status, aim.NextStep, evidence, aim.Confidence, to, saved.ID).Scan(&id); err != nil {
				return saved, err
			}
		}
		touched = append(touched, id)
		if position == reading.Now {
			now := id
			saved.NowIntentID = &now
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE intents SET status = 'paused', reading_id = $1
		WHERE status IN ('active', 'waiting') AND last_seen < $2 AND NOT (id = ANY($3))`, saved.ID, from, touched); err != nil {
		return saved, err
	}
	if _, err := tx.Exec(ctx, `UPDATE intent_readings SET now_intent_id = $2 WHERE id = $1`, saved.ID, saved.NowIntentID); err != nil {
		return saved, err
	}
	return saved, tx.Commit(ctx)
}

// ListIntents returns intents seen since a time, and any still active or
// waiting, most recently seen first.
func (p *Postgres) ListIntents(ctx context.Context, since time.Time) ([]Intent, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, title, kind, status, next_step, evidence, confidence, first_seen, last_seen, reading_id FROM intents
		WHERE last_seen >= $1 OR status IN ('active', 'waiting') ORDER BY last_seen DESC, id DESC LIMIT 200`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Intent{}
	for rows.Next() {
		var item Intent
		var evidence []byte
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.Status, &item.NextStep, &evidence, &item.Confidence, &item.FirstSeen, &item.LastSeen, &item.ReadingID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidence, &item.Evidence); err != nil {
			return nil, err
		}
		item.Evidence = nonNil(item.Evidence)
		result = append(result, item)
	}
	return result, rows.Err()
}

// LatestIntentReading returns the newest reading, or nil before the first.
func (p *Postgres) LatestIntentReading(ctx context.Context) (*IntentReading, error) {
	var reading IntentReading
	err := p.pool.QueryRow(ctx, `SELECT id, read_at, window_from, window_to, note, now_intent_id FROM intent_readings ORDER BY read_at DESC, id DESC LIMIT 1`).
		Scan(&reading.ID, &reading.ReadAt, &reading.From, &reading.To, &reading.Note, &reading.NowIntentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &reading, nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
