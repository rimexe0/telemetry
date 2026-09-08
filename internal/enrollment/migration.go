package enrollment

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The enrollment migration intentionally lives beside this package. The core
// store's migration runner does not need to know about device credentials; the
// application starts enrollment explicitly after the core schema is ready.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationName = "0003_device_enrollment.sql"

// Migrate creates the enrollment schema and records it in the same migration
// ledger used by the core store. It is safe to call on every service start.
func (s *Service) Migrate(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("enrollment service is not initialized")
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
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
