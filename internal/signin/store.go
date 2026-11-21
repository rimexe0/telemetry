package signin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The sign-in migration lives beside this package, like enrollment's, and is
// recorded in the same schema_migrations ledger as the core store.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

const settingsKey = "sign_in"

// Change is one applied sign-in change, kept permanently.
type Change struct {
	ChangedAt time.Time `json:"changed_at"`
	Change    string    `json:"change"`
	Previous  Settings  `json:"previous"`
	Value     Settings  `json:"value"`
}

// Store reads and changes the sign-in settings.
type Store interface {
	// Load returns the stored settings, or Defaults when none were saved.
	Load(ctx context.Context) (Settings, error)
	// Update applies mutate to the current settings, validates the result and
	// saves it with a history row naming the change and what it replaced.
	Update(ctx context.Context, change string, mutate func(Settings) (Settings, error)) (Settings, error)
	// Changes returns the most recent changes, newest first.
	Changes(ctx context.Context, limit int) ([]Change, error)
}

// Postgres is the Store used by telemetryd.
type Postgres struct {
	pool *pgxpool.Pool
}

// OpenStore connects to the database. It does not create the tables; run
// Migrate (telemetryd -migrate) for that.
func OpenStore(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

// NotMigrated reports whether err means the sign-in tables do not exist yet.
func NotMigrated(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "42P01"
}

// Migrate creates the sign-in tables. It is safe to run repeatedly.
func (p *Postgres) Migrate(ctx context.Context) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL)`); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES ($1, $2)`, entry.Name(), time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Load(ctx context.Context) (Settings, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT value FROM telemetry_settings WHERE key = $1`, settingsKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return decodeSettings(raw)
}

func (p *Postgres) Update(ctx context.Context, change string, mutate func(Settings) (Settings, error)) (Settings, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback(ctx)
	defaults, err := json.Marshal(Defaults())
	if err != nil {
		return Settings{}, err
	}
	// Insert the defaults first so concurrent first changes lock one row.
	if _, err := tx.Exec(ctx, `INSERT INTO telemetry_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, settingsKey, defaults); err != nil {
		return Settings{}, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT value FROM telemetry_settings WHERE key = $1 FOR UPDATE`, settingsKey).Scan(&raw); err != nil {
		return Settings{}, err
	}
	previous, err := decodeSettings(raw)
	if err != nil {
		return Settings{}, err
	}
	next, err := mutate(previous)
	if err != nil {
		return Settings{}, err
	}
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}
	if next == previous {
		return next, tx.Commit(ctx)
	}
	value, err := json.Marshal(next)
	if err != nil {
		return Settings{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE telemetry_settings SET value = $2, updated_at = now() WHERE key = $1`, settingsKey, value); err != nil {
		return Settings{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telemetry_settings_changes (key, change, previous, value) VALUES ($1, $2, $3, $4)`, settingsKey, change, raw, value); err != nil {
		return Settings{}, err
	}
	return next, tx.Commit(ctx)
}

func (p *Postgres) Changes(ctx context.Context, limit int) ([]Change, error) {
	rows, err := p.pool.Query(ctx, `SELECT changed_at, change, previous, value FROM telemetry_settings_changes WHERE key = $1 ORDER BY id DESC LIMIT $2`, settingsKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []Change{}
	for rows.Next() {
		var item Change
		var previous, value []byte
		if err := rows.Scan(&item.ChangedAt, &item.Change, &previous, &value); err != nil {
			return nil, err
		}
		if item.Previous, err = decodeSettings(previous); err != nil {
			return nil, err
		}
		if item.Value, err = decodeSettings(value); err != nil {
			return nil, err
		}
		changes = append(changes, item)
	}
	return changes, rows.Err()
}

func decodeSettings(raw []byte) (Settings, error) {
	var value Settings
	if err := json.Unmarshal(raw, &value); err != nil {
		return Settings{}, fmt.Errorf("decode sign-in settings: %w", err)
	}
	return value, nil
}
