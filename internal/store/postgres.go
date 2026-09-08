package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rimexe0/telemetry/internal/model"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var (
	ErrBatchConflict          = errors.New("batch id was previously used with different content")
	ErrSourceIdentityConflict = errors.New("source instance key conflicts with its registered device or source kind")
)

type Postgres struct {
	pool *pgxpool.Pool
}

func OpenPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

// ApplyMigrations is explicit so deployment owns when schema changes occur.
func (p *Postgres) ApplyMigrations(ctx context.Context) error {
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

func (p *Postgres) Ingest(ctx context.Context, batch model.Batch) (Receipt, error) {
	if err := batch.Normalize(); err != nil {
		return Receipt{}, err
	}
	encodedBatch, err := json.Marshal(batch)
	if err != nil {
		return Receipt{}, fmt.Errorf("encode batch: %w", err)
	}
	digestBytes := sha256.Sum256(encodedBatch)
	digest := hex.EncodeToString(digestBytes[:])
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Receipt{}, err
	}
	defer tx.Rollback(ctx)
	var existingDigest string
	err = tx.QueryRow(ctx, `SELECT request_digest FROM ingestion_batches WHERE batch_id = $1`, batch.ID).Scan(&existingDigest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, err
	}
	if existingDigest != "" && existingDigest != digest {
		return Receipt{}, ErrBatchConflict
	}
	if existingDigest == "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ingestion_batches (batch_id, device_id, collector_version, request_digest, received_at)
			VALUES ($1, $2, $3, $4, $5)`,
			batch.ID, batch.DeviceID, batch.CollectorVersion, digest, time.Now().UTC()); err != nil {
			return Receipt{}, err
		}
	}
	receipt := Receipt{BatchID: batch.ID, ObservationsSeen: len(batch.Observations)}
	for _, observation := range batch.Observations {
		var sourceInstanceKey string
		if err := tx.QueryRow(ctx, `
			INSERT INTO source_instances (source_instance_key, source_kind, device_id, first_collected_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (source_instance_key) DO UPDATE
			SET source_kind = source_instances.source_kind
			WHERE source_instances.source_kind = EXCLUDED.source_kind
			  AND source_instances.device_id = EXCLUDED.device_id
			RETURNING source_instance_key`,
			observation.SourceInstanceKey, observation.SourceKind, observation.DeviceID, observation.CollectedAt,
		).Scan(&sourceInstanceKey); errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, ErrSourceIdentityConflict
		} else if err != nil {
			return Receipt{}, err
		}
		var observationID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO observations (
				source_instance_key, source_record_key, kind, occurred_at, ended_at, collected_at
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (source_instance_key, source_record_key) DO UPDATE
			SET collected_at = GREATEST(observations.collected_at, EXCLUDED.collected_at)
			RETURNING id`,
			observation.SourceInstanceKey, observation.SourceRecordKey, observation.Kind,
			observation.OccurredAt, observation.EndedAt, observation.CollectedAt,
		).Scan(&observationID); err != nil {
			return Receipt{}, err
		}
		var revisionID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO observation_revisions (
				observation_id, revision_digest, kind, occurred_at, ended_at,
				original_payload, projection, collected_at, source_schema_version, timezone
			) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)
			ON CONFLICT (observation_id, revision_digest) DO NOTHING
			RETURNING id`,
			observationID, observation.RevisionDigest, observation.Kind, observation.OccurredAt, observation.EndedAt,
			[]byte(observation.OriginalPayload), string(observation.Projection), observation.CollectedAt,
			observation.SourceSchemaVersion, observation.Timezone,
		).Scan(&revisionID)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx, `
				SELECT id FROM observation_revisions WHERE observation_id = $1 AND revision_digest = $2`,
				observationID, observation.RevisionDigest,
			).Scan(&revisionID); err != nil {
				return Receipt{}, err
			}
		} else if err != nil {
			return Receipt{}, err
		} else {
			receipt.RevisionsCreated++
			if _, err := tx.Exec(ctx, `
				UPDATE observations
				SET effective_revision_id = $1, kind = $2, occurred_at = $3, ended_at = $4,
					collected_at = GREATEST(collected_at, $5)
				WHERE id = $6`,
				revisionID, observation.Kind, observation.OccurredAt, observation.EndedAt,
				observation.CollectedAt, observationID,
			); err != nil {
				return Receipt{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ingestion_batch_observations (batch_id, observation_revision_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, batch.ID, revisionID); err != nil {
			return Receipt{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (p *Postgres) QueryObservations(ctx context.Context, query Query) ([]ObservationView, error) {
	if query.Limit <= 0 || query.Limit > 1000 {
		return nil, errors.New("query limit must be between 1 and 1000")
	}
	clauses := []string{"o.effective_revision_id IS NOT NULL"}
	args := []any{}
	if query.SourceInstanceKey != "" {
		args = append(args, query.SourceInstanceKey)
		clauses = append(clauses, fmt.Sprintf("o.source_instance_key = $%d", len(args)))
	}
	if query.From != nil {
		args = append(args, query.From.UTC())
		clauses = append(clauses, fmt.Sprintf("o.occurred_at >= $%d", len(args)))
	}
	if query.To != nil {
		args = append(args, query.To.UTC())
		clauses = append(clauses, fmt.Sprintf("o.occurred_at < $%d", len(args)))
	}
	args = append(args, query.Limit)
	statement := `
		SELECT si.source_instance_key, si.source_kind, si.device_id,
			o.source_record_key, r.kind, r.occurred_at, r.ended_at, o.collected_at,
			r.revision_digest, r.original_payload, r.projection
		FROM observations o
		JOIN source_instances si ON si.source_instance_key = o.source_instance_key
		JOIN observation_revisions r ON r.id = o.effective_revision_id
		WHERE ` + strings.Join(clauses, " AND ") + fmt.Sprintf(" ORDER BY o.occurred_at, o.id LIMIT $%d", len(args))
	rows, err := p.pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]ObservationView, 0)
	for rows.Next() {
		var value ObservationView
		if err := rows.Scan(
			&value.SourceInstanceKey, &value.SourceKind, &value.DeviceID,
			&value.SourceRecordKey, &value.Kind, &value.OccurredAt, &value.EndedAt, &value.CollectedAt,
			&value.RevisionDigest, &value.OriginalPayload, &value.Projection,
		); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (p *Postgres) ListSources(ctx context.Context, limit int) ([]SourceSummary, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("source limit must be between 1 and 1000")
	}
	rows, err := p.pool.Query(ctx, `
		SELECT si.source_instance_key, si.source_kind, si.device_id, si.first_collected_at,
			MAX(o.collected_at), COUNT(o.id)
		FROM source_instances si
		LEFT JOIN observations o ON o.source_instance_key = si.source_instance_key
		GROUP BY si.source_instance_key
		ORDER BY MAX(o.collected_at) DESC NULLS LAST, si.source_instance_key
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]SourceSummary, 0)
	for rows.Next() {
		var value SourceSummary
		if err := rows.Scan(&value.SourceInstanceKey, &value.SourceKind, &value.DeviceID, &value.FirstCollectedAt, &value.LastCollectedAt, &value.ObservationCount); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
