// Package activitywatch adapts ActivityWatch's versioned REST records without
// flattening their source-specific payload fields.
package activitywatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Bucket struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Hostname string          `json:"hostname"`
	Created  *time.Time      `json:"created,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type Event struct {
	ID        json.RawMessage `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Duration  *float64        `json:"duration,omitempty"`
	Data      json.RawMessage `json:"data"`
	Raw       json.RawMessage `json:"-"`
}

func (c Client) Info(ctx context.Context) (json.RawMessage, error) {
	return c.getObject(ctx, "/api/0/info")
}

func (c Client) Buckets(ctx context.Context) ([]Bucket, error) {
	body, err := c.getObject(ctx, "/api/0/buckets")
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode ActivityWatch buckets: %w", err)
	}
	buckets := make([]Bucket, 0, len(raw))
	for id, payload := range raw {
		var bucket Bucket
		if err := json.Unmarshal(payload, &bucket); err != nil {
			return nil, fmt.Errorf("decode ActivityWatch bucket %q: %w", id, err)
		}
		bucket.ID = id
		buckets = append(buckets, bucket)
	}
	return buckets, nil
}

func (c Client) Events(ctx context.Context, bucketID string, from, to time.Time, limit int) ([]Event, error) {
	if bucketID == "" {
		return nil, errors.New("ActivityWatch bucket id is required")
	}
	if limit <= 0 {
		return nil, errors.New("ActivityWatch event limit must be positive")
	}
	path := "/api/0/buckets/" + url.PathEscape(bucketID) + "/events"
	query := url.Values{}
	query.Set("start", from.UTC().Format(time.RFC3339Nano))
	query.Set("end", to.UTC().Format(time.RFC3339Nano))
	query.Set("limit", strconv.Itoa(limit))
	body, err := c.get(ctx, path+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, fmt.Errorf("decode ActivityWatch events: %w", err)
	}
	events := make([]Event, 0, len(raws))
	for _, raw := range raws {
		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("decode ActivityWatch event: %w", err)
		}
		if event.Timestamp.IsZero() || len(event.Data) == 0 {
			return nil, errors.New("ActivityWatch event is missing timestamp or data")
		}
		event.Timestamp = event.Timestamp.UTC()
		event.Raw = append([]byte(nil), raw...)
		events = append(events, event)
	}
	return events, nil
}

func (c Client) getObject(ctx context.Context, path string) (json.RawMessage, error) {
	body, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("ActivityWatch response is not an object: %w", err)
	}
	return body, nil
}

func (c Client) get(ctx context.Context, path string) ([]byte, error) {
	if c.BaseURL == "" {
		return nil, errors.New("ActivityWatch base URL is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request ActivityWatch: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ActivityWatch returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

type SyncOptions struct {
	DeviceID string
	Bucket   Bucket
	From     *time.Time
	Until    *time.Time
	Overlap  time.Duration
	Limit    int
	Now      time.Time
}

type SyncResult struct {
	EventsSeen           int
	RevisionsEnqueued    int
	UsedFallbackIdentity int
	CursorAdvanced       bool
	Truncated            bool
}

// BackfillOptions divides an explicit historical interval into bounded windows.
// A completed window is recorded in the same durable cursor used by incremental
// sync, so an interrupted backfill resumes from its last safe boundary.
type BackfillOptions struct {
	DeviceID string
	Bucket   Bucket
	From     time.Time
	To       time.Time
	Window   time.Duration
	Overlap  time.Duration
	Limit    int
	Now      time.Time
}

type BackfillResult struct {
	WindowsSynced        int
	WindowsNarrowed      int
	EventsSeen           int
	RevisionsEnqueued    int
	UsedFallbackIdentity int
	AlreadyCovered       bool
}

// BackfillBucket imports an explicit historical range without asking the source
// for an unbounded response. A full response leaves that window's cursor
// unchanged and narrows the same range, preventing a later window from hiding
// data while allowing a dense historical period to resume automatically.
func BackfillBucket(ctx context.Context, client Client, localQueue *queue.Queue, options BackfillOptions) (BackfillResult, error) {
	if localQueue == nil || options.DeviceID == "" || options.Bucket.ID == "" {
		return BackfillResult{}, errors.New("queue, device ID, and bucket ID are required")
	}
	if options.From.IsZero() || options.To.IsZero() || !options.From.Before(options.To) {
		return BackfillResult{}, errors.New("ActivityWatch backfill requires from before to")
	}
	if options.Window <= 0 || options.Limit <= 0 || options.Overlap < 0 {
		return BackfillResult{}, errors.New("ActivityWatch backfill window, limit, or overlap is invalid")
	}
	collectedAt := options.Now.UTC()
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	cursorKey := "activitywatch.watermark." + options.DeviceID + "." + options.Bucket.ID
	current := options.From.UTC()
	if saved, found, err := localQueue.LoadCursor(ctx, cursorKey); err != nil {
		return BackfillResult{}, err
	} else if found {
		watermark, err := time.Parse(time.RFC3339Nano, saved)
		if err != nil {
			return BackfillResult{}, fmt.Errorf("parse ActivityWatch watermark: %w", err)
		}
		if watermark.After(current) {
			current = watermark.UTC()
		}
	}
	if !current.Before(options.To.UTC()) {
		return BackfillResult{AlreadyCovered: true}, nil
	}
	result := BackfillResult{}
	window := options.Window
	for current.Before(options.To.UTC()) {
		next := current.Add(window)
		if next.After(options.To.UTC()) {
			next = options.To.UTC()
		}
		start := current
		syncResult, err := SyncBucket(ctx, client, localQueue, SyncOptions{
			DeviceID: options.DeviceID,
			Bucket:   options.Bucket,
			From:     &start,
			Until:    &next,
			Overlap:  options.Overlap,
			Limit:    options.Limit,
			Now:      collectedAt,
		})
		result.EventsSeen += syncResult.EventsSeen
		result.RevisionsEnqueued += syncResult.RevisionsEnqueued
		result.UsedFallbackIdentity += syncResult.UsedFallbackIdentity
		if err != nil {
			return result, err
		}
		if syncResult.Truncated {
			result.WindowsNarrowed++
			if window <= time.Second {
				return result, fmt.Errorf("ActivityWatch backfill window %s through %s reached limit %d at one-second resolution", start.Format(time.RFC3339), next.Format(time.RFC3339), options.Limit)
			}
			window /= 2
			if window < time.Second {
				window = time.Second
			}
			continue
		}
		result.WindowsSynced++
		current = next
	}
	return result, nil
}

// SyncBucket queues one selected bucket. It only advances the watermark after
// the observations are durable locally, and refuses to advance after a capped
// response that could hide older events.
func SyncBucket(ctx context.Context, client Client, localQueue *queue.Queue, options SyncOptions) (SyncResult, error) {
	if localQueue == nil || options.DeviceID == "" || options.Bucket.ID == "" {
		return SyncResult{}, errors.New("queue, device ID, and bucket ID are required")
	}
	if options.Limit <= 0 || options.Overlap < 0 {
		return SyncResult{}, errors.New("event limit and overlap are invalid")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	until := options.Now.UTC()
	if options.Until != nil {
		until = options.Until.UTC()
	}
	cursorKey := "activitywatch.watermark." + options.DeviceID + "." + options.Bucket.ID
	from := options.From
	if saved, found, err := localQueue.LoadCursor(ctx, cursorKey); err != nil {
		return SyncResult{}, err
	} else if found {
		watermark, err := time.Parse(time.RFC3339Nano, saved)
		if err != nil {
			return SyncResult{}, fmt.Errorf("parse ActivityWatch watermark: %w", err)
		}
		value := watermark.Add(-options.Overlap)
		from = &value
	}
	if from == nil {
		return SyncResult{}, errors.New("initial ActivityWatch sync requires an explicit from time")
	}
	if !from.Before(until) {
		return SyncResult{}, errors.New("ActivityWatch from time must precede now")
	}
	events, err := client.Events(ctx, options.Bucket.ID, *from, until, options.Limit)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{EventsSeen: len(events), Truncated: len(events) >= options.Limit}
	for _, event := range events {
		observation, fallback, err := ObservationFromEvent(options.DeviceID, options.Bucket, event, options.Now)
		if err != nil {
			return result, err
		}
		if fallback {
			result.UsedFallbackIdentity++
		}
		inserted, err := localQueue.Enqueue(ctx, observation, options.Now)
		if err != nil {
			return result, err
		}
		if inserted {
			result.RevisionsEnqueued++
		}
	}
	if result.Truncated {
		return result, nil
	}
	if err := localQueue.SaveCursor(ctx, cursorKey, until.Format(time.RFC3339Nano), options.Now); err != nil {
		return result, err
	}
	result.CursorAdvanced = true
	return result, nil
}

// ObservationFromEvent keeps ActivityWatch's complete event object as the raw
// source representation. A server-assigned event id is the revision anchor.
func ObservationFromEvent(deviceID string, bucket Bucket, event Event, collectedAt time.Time) (model.Observation, bool, error) {
	if deviceID == "" || bucket.ID == "" || event.Timestamp.IsZero() || len(event.Raw) == 0 {
		return model.Observation{}, false, errors.New("invalid ActivityWatch event identity")
	}
	recordKey, fallback := activityWatchRecordKey(event)
	observation := model.Observation{
		SourceInstanceKey:   "activitywatch:" + deviceID + ":" + bucket.ID,
		SourceKind:          "activitywatch",
		DeviceID:            deviceID,
		SourceRecordKey:     recordKey,
		Kind:                "activitywatch.event",
		OccurredAt:          event.Timestamp.UTC(),
		CollectedAt:         collectedAt.UTC(),
		SourceSchemaVersion: "activitywatch-api-0",
		OriginalPayload:     append([]byte(nil), event.Raw...),
	}
	if event.Duration != nil {
		end := observation.OccurredAt.Add(time.Duration(*event.Duration * float64(time.Second)))
		observation.EndedAt = &end
	}
	if err := observation.Normalize(); err != nil {
		return model.Observation{}, false, err
	}
	return observation, fallback, nil
}

func activityWatchRecordKey(event Event) (string, bool) {
	if len(event.ID) > 0 && string(event.ID) != "null" {
		return "id:" + string(event.ID), false
	}
	digest := sha256.Sum256(event.Raw)
	// This cannot reconcile a later change to an event without an assigned id.
	// The caller reports the fallback so it is not mistaken for full coverage.
	return "unidentified:" + hex.EncodeToString(digest[:]), true
}
