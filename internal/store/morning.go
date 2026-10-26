package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func morningHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// CreateMorningCode makes a new morning code and retires the earlier ones,
// so only the newest printed code opens the morning. The code is returned
// once and only its hash is kept.
func (p *Postgres) CreateMorningCode(ctx context.Context) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(random)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE morning_codes SET retired_at = now() WHERE retired_at IS NULL`); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO morning_codes (code_hash) VALUES ($1)`, morningHash(code)); err != nil {
		return "", err
	}
	return code, tx.Commit(ctx)
}

// ScanMorningCode records a scan of the current morning code. A code that
// is not the current one records nothing and reports false.
func (p *Postgres) ScanMorningCode(ctx context.Context, code string) (bool, error) {
	if code == "" || len(code) > 200 {
		return false, nil
	}
	tag, err := p.pool.Exec(ctx, `INSERT INTO morning_scans (code_id) SELECT id FROM morning_codes WHERE code_hash = $1 AND retired_at IS NULL`, morningHash(code))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MorningCodeMade is when the current morning code was made, or nil when
// there is none.
func (p *Postgres) MorningCodeMade(ctx context.Context) (*time.Time, error) {
	var made time.Time
	err := p.pool.QueryRow(ctx, `SELECT created_at FROM morning_codes WHERE retired_at IS NULL ORDER BY id DESC LIMIT 1`).Scan(&made)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &made, nil
}

// LatestMorningScan is the latest scan at or before a time, or nil.
func (p *Postgres) LatestMorningScan(ctx context.Context, at time.Time) (*time.Time, error) {
	var scanned time.Time
	err := p.pool.QueryRow(ctx, `SELECT scanned_at FROM morning_scans WHERE scanned_at <= $1 ORDER BY scanned_at DESC LIMIT 1`, at).Scan(&scanned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scanned, nil
}
