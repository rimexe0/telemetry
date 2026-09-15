package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	defaultCollectionHealthSourceLimit = 100
	maxCollectionHealthSourceLimit     = 1000
	defaultCollectionHealthSamples     = 24
	maxCollectionHealthSamples         = 72
)

// CollectionHealth reads source coverage and the latest resource status
// observations in one read-only snapshot. Resource observations are filtered
// by their registered source kind and observation kind so arbitrary source
// payloads are never included in this operational view.
func (p *Postgres) CollectionHealth(ctx context.Context, query CollectionHealthQuery) (CollectionHealth, error) {
	if query.SourceLimit == 0 {
		query.SourceLimit = defaultCollectionHealthSourceLimit
	}
	if query.ResourceSamples == 0 {
		query.ResourceSamples = defaultCollectionHealthSamples
	}
	if query.SourceLimit < 1 || query.SourceLimit > maxCollectionHealthSourceLimit {
		return CollectionHealth{}, errors.New("collection health source limit must be between 1 and 1000")
	}
	if query.ResourceSamples < 1 || query.ResourceSamples > maxCollectionHealthSamples {
		return CollectionHealth{}, errors.New("collection health samples must be between 1 and 72")
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CollectionHealth{}, err
	}
	defer tx.Rollback(ctx)
	// The source summary GROUP BY can otherwise request more PostgreSQL shared
	// memory than the small central host has available. This is transaction
	// local and does not change the server configuration.
	if _, err := tx.Exec(ctx, `SET LOCAL max_parallel_workers_per_gather = 0`); err != nil {
		return CollectionHealth{}, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '20s'`); err != nil {
		return CollectionHealth{}, err
	}

	result := CollectionHealth{
		CheckedAt: time.Now().UTC(),
		Sources:   make([]SourceSummary, 0, query.SourceLimit),
		Devices:   make([]CollectionHealthDevice, 0),
	}
	sources, err := tx.Query(ctx, `
		SELECT si.source_instance_key, si.source_kind, si.device_id, si.first_collected_at,
			MAX(o.collected_at), COUNT(o.id)
		FROM source_instances si
		LEFT JOIN observations o ON o.source_instance_key = si.source_instance_key
		GROUP BY si.source_instance_key
		ORDER BY MAX(o.collected_at) DESC NULLS LAST, si.source_instance_key
		LIMIT $1`, query.SourceLimit+1)
	if err != nil {
		return CollectionHealth{}, err
	}
	for sources.Next() {
		var source SourceSummary
		if err := sources.Scan(
			&source.SourceInstanceKey, &source.SourceKind, &source.DeviceID,
			&source.FirstCollectedAt, &source.LastCollectedAt, &source.ObservationCount,
		); err != nil {
			sources.Close()
			return CollectionHealth{}, err
		}
		result.Sources = append(result.Sources, source)
	}
	if err := sources.Err(); err != nil {
		sources.Close()
		return CollectionHealth{}, err
	}
	sources.Close()
	if len(result.Sources) > query.SourceLimit {
		result.SourcesTruncated = true
		result.Sources = result.Sources[:query.SourceLimit]
	}

	deviceSet := make(map[string]struct{}, len(result.Sources))
	for _, source := range result.Sources {
		if source.DeviceID != "" {
			deviceSet[source.DeviceID] = struct{}{}
		}
	}
	deviceIDs := make([]string, 0, len(deviceSet))
	for deviceID := range deviceSet {
		deviceIDs = append(deviceIDs, deviceID)
	}
	sort.Strings(deviceIDs)
	result.Devices = make([]CollectionHealthDevice, len(deviceIDs))
	deviceIndex := make(map[string]int, len(deviceIDs))
	for i, deviceID := range deviceIDs {
		deviceIndex[deviceID] = i
		result.Devices[i] = CollectionHealthDevice{
			DeviceID:        deviceID,
			ResourceHistory: make([]ObservationView, 0, query.ResourceSamples),
		}
	}
	if len(deviceIDs) == 0 {
		return result, tx.Commit(ctx)
	}

	placeholders := numberedPlaceholders(1, len(deviceIDs))
	deviceValues := make([]string, len(placeholders))
	for i, placeholder := range placeholders {
		deviceValues[i] = "(" + placeholder + ")"
	}
	resourceArgs := make([]any, 0, len(deviceIDs)+1)
	for _, deviceID := range deviceIDs {
		resourceArgs = append(resourceArgs, deviceID)
	}
	resourceArgs = append(resourceArgs, query.ResourceSamples)
	resourceRows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT selected_devices.device_id, resource.source_instance_key,
			resource.source_kind, resource.source_record_key, resource.kind,
			resource.occurred_at, resource.ended_at, resource.collected_at,
			resource.revision_digest, resource.original_payload, resource.projection,
			resource.observation_id
		FROM (VALUES %s) AS selected_devices(device_id)
		CROSS JOIN LATERAL (
			SELECT o.id AS observation_id,
				si.source_instance_key, si.source_kind,
				o.source_record_key, r.kind, r.occurred_at, r.ended_at,
				o.collected_at, r.revision_digest, r.original_payload, r.projection
			FROM observations o
			JOIN source_instances si ON si.source_instance_key = o.source_instance_key
			JOIN observation_revisions r ON r.id = o.effective_revision_id
			WHERE o.effective_revision_id IS NOT NULL
				AND si.source_kind = 'telemetry_collector'
				AND r.kind = 'telemetry.collector_status'
				AND si.device_id = selected_devices.device_id
			ORDER BY o.collected_at DESC, o.id DESC
			LIMIT $%d
		) AS resource
		ORDER BY selected_devices.device_id, resource.collected_at DESC, resource.observation_id DESC`,
		strings.Join(deviceValues, ","), len(resourceArgs)), resourceArgs...)
	if err != nil {
		return CollectionHealth{}, err
	}
	for resourceRows.Next() {
		var deviceID string
		var observationID int64
		var resource ObservationView
		if err := resourceRows.Scan(
			&deviceID, &resource.SourceInstanceKey, &resource.SourceKind,
			&resource.SourceRecordKey, &resource.Kind, &resource.OccurredAt,
			&resource.EndedAt, &resource.CollectedAt, &resource.RevisionDigest,
			&resource.OriginalPayload, &resource.Projection, &observationID,
		); err != nil {
			resourceRows.Close()
			return CollectionHealth{}, err
		}
		index, ok := deviceIndex[deviceID]
		if ok {
			resource.DeviceID = deviceID
			result.Devices[index].ResourceHistory = append(result.Devices[index].ResourceHistory, resource)
		}
	}
	if err := resourceRows.Err(); err != nil {
		resourceRows.Close()
		return CollectionHealth{}, err
	}
	resourceRows.Close()

	receivedArgs := make([]any, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		receivedArgs = append(receivedArgs, deviceID)
	}
	receivedRows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT device_id, MAX(received_at)
		FROM ingestion_batches
		WHERE device_id IN (%s)
		GROUP BY device_id`, strings.Join(numberedPlaceholders(1, len(deviceIDs)), ",")), receivedArgs...)
	if err != nil {
		return CollectionHealth{}, err
	}
	for receivedRows.Next() {
		var deviceID string
		var receivedAt time.Time
		if err := receivedRows.Scan(&deviceID, &receivedAt); err != nil {
			receivedRows.Close()
			return CollectionHealth{}, err
		}
		if index, ok := deviceIndex[deviceID]; ok {
			receivedAt = receivedAt.UTC()
			result.Devices[index].LastReceivedAt = &receivedAt
		}
	}
	if err := receivedRows.Err(); err != nil {
		receivedRows.Close()
		return CollectionHealth{}, err
	}
	receivedRows.Close()
	return result, tx.Commit(ctx)
}

func numberedPlaceholders(start, count int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = fmt.Sprintf("$%d", start+i)
	}
	return values
}
