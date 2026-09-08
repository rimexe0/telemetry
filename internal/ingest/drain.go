package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

type DrainOptions struct {
	Limit         int
	LeaseDuration time.Duration
	RetryAfter    time.Duration
	Now           time.Time
	DeviceID      string
	Version       string
}

type DrainResult struct {
	Claimed       int
	Acknowledged  int
	BatchID       string
	RevisionsMade int
}

type DrainAllOptions struct {
	DrainOptions
	MaxBatches int
}

type DrainAllResult struct {
	Batches        int
	Claimed        int
	Acknowledged   int
	RevisionsMade  int
	QueueExhausted bool
}

// Drain performs one bounded delivery attempt. Local acknowledgement happens
// only after the central service has returned a matching committed receipt.
func Drain(ctx context.Context, queueStore *queue.Queue, client Client, options DrainOptions) (DrainResult, error) {
	if queueStore == nil {
		return DrainResult{}, errors.New("queue is required")
	}
	if options.Limit <= 0 || options.LeaseDuration <= 0 || options.RetryAfter < 0 {
		return DrainResult{}, errors.New("limit, lease duration, and retry delay are invalid")
	}
	if options.DeviceID == "" {
		return DrainResult{}, errors.New("device id is required")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	items, err := queueStore.Claim(ctx, options.Limit, options.LeaseDuration, options.Now)
	if err != nil {
		return DrainResult{}, err
	}
	result := DrainResult{Claimed: len(items)}
	if len(items) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(items))
	observations := make([]model.Observation, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		observations = append(observations, item.Observation)
	}
	batchID, err := model.DeliveryBatchID(ids)
	if err != nil {
		return result, err
	}
	result.BatchID = batchID
	batch := model.Batch{ID: batchID, DeviceID: options.DeviceID, CollectorVersion: options.Version, Observations: observations}
	receipt, err := client.Send(ctx, batch)
	if err != nil {
		for _, item := range items {
			if releaseErr := queueStore.Release(ctx, item, options.Now.Add(options.RetryAfter), err); releaseErr != nil && !errors.Is(releaseErr, queue.ErrLeaseNotHeld) {
				return result, fmt.Errorf("delivery failed (%w); release failed: %v", err, releaseErr)
			}
		}
		return result, err
	}
	result.RevisionsMade = receipt.RevisionsCreated
	for _, item := range items {
		if err := queueStore.Acknowledge(ctx, item, options.Now); err != nil {
			// The central write is already durable. Leave this local row leased so
			// a later retry converges through central idempotency rather than
			// pretending acknowledgement happened.
			return result, fmt.Errorf("central batch %s accepted but local acknowledgement failed: %w", batchID, err)
		}
		result.Acknowledged++
	}
	return result, nil
}

// DrainAll delivers bounded batches sequentially. It does not retain earlier
// batch payloads in memory, and it stops on the first failed central receipt so
// the queue's configured retry policy remains observable.
func DrainAll(ctx context.Context, queueStore *queue.Queue, client Client, options DrainAllOptions) (DrainAllResult, error) {
	if options.MaxBatches <= 0 {
		return DrainAllResult{}, errors.New("maximum batches must be positive")
	}
	result := DrainAllResult{}
	for result.Batches < options.MaxBatches {
		drainOptions := options.DrainOptions
		if drainOptions.Now.IsZero() {
			drainOptions.Now = time.Now().UTC()
		}
		batch, err := Drain(ctx, queueStore, client, drainOptions)
		if err != nil {
			return result, err
		}
		if batch.Claimed == 0 {
			result.QueueExhausted = true
			return result, nil
		}
		result.Batches++
		result.Claimed += batch.Claimed
		result.Acknowledged += batch.Acknowledged
		result.RevisionsMade += batch.RevisionsMade
		options.DrainOptions.Now = time.Time{}
	}
	return result, fmt.Errorf("delivery stopped after configured maximum of %d batches", options.MaxBatches)
}
