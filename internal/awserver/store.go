// Package awserver keeps ActivityWatch-shaped buckets and events for the
// Telemetry desktop collector. It replaces aw-server for the clients the owner
// uses (the ActivityWatch browser extension and aw-watcher-codex) and for the
// collector's own window and AFK watchers. Events are not stored for querying;
// every new or changed event goes straight into the delivery queue.
package awserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/queue"
)

// SchemaVersion marks observations produced by this collector. The payload
// keeps the ActivityWatch event shape so existing consumers read it unchanged.
const SchemaVersion = "telemetry-desktop-aw-api-1"

var (
	ErrNoBucket     = errors.New("bucket does not exist")
	ErrInvalidEvent = errors.New("invalid event")
)

type Bucket struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Client      string          `json:"client"`
	Hostname    string          `json:"hostname"`
	Created     time.Time       `json:"created"`
	Data        json.RawMessage `json:"data,omitempty"`
	LastUpdated *time.Time      `json:"last_updated,omitempty"`
}

type Event struct {
	ID        int64           `json:"id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Duration  float64         `json:"duration"`
	Data      json.RawMessage `json:"data"`
}

func (e Event) end() time.Time {
	return e.Timestamp.Add(time.Duration(e.Duration * float64(time.Second)))
}

type bucketState struct {
	Bucket Bucket `json:"bucket"`
	// Open is the event heartbeats may still extend.
	Open *Event `json:"open,omitempty"`
	// OpenKey is the record key of Open; OpenDirty means it changed since it
	// was last queued.
	OpenKey   string `json:"open_key,omitempty"`
	OpenDirty bool   `json:"open_dirty,omitempty"`
}

// Store is safe for concurrent use by the HTTP handler and the watchers.
type Store struct {
	mu       sync.Mutex
	queue    *queue.Queue
	deviceID string
	now      func() time.Time
	buckets  map[string]*bucketState
	nextID   int64
}

const (
	cursorBuckets = "awserver:buckets"
	cursorNextID  = "awserver:next_id"
)

// Open loads buckets and open events saved by an earlier run.
func Open(ctx context.Context, q *queue.Queue, deviceID string, now func() time.Time) (*Store, error) {
	if q == nil || deviceID == "" {
		return nil, errors.New("queue and device ID are required")
	}
	if now == nil {
		now = time.Now
	}
	store := &Store{queue: q, deviceID: deviceID, now: now, buckets: map[string]*bucketState{}, nextID: 1}
	if value, ok, err := q.LoadCursor(ctx, cursorNextID); err != nil {
		return nil, err
	} else if ok {
		if store.nextID, err = strconv.ParseInt(value, 10, 64); err != nil {
			return nil, fmt.Errorf("saved event id: %w", err)
		}
	}
	if value, ok, err := q.LoadCursor(ctx, cursorBuckets); err != nil {
		return nil, err
	} else if ok {
		if err := json.Unmarshal([]byte(value), &store.buckets); err != nil {
			return nil, fmt.Errorf("saved buckets: %w", err)
		}
	}
	return store, nil
}

// EnsureBucket creates a bucket; it reports false when it already existed.
func (s *Store) EnsureBucket(ctx context.Context, bucket Bucket) (bool, error) {
	if bucket.ID == "" {
		return false, errors.New("bucket id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[bucket.ID]; ok {
		return false, nil
	}
	if bucket.Created.IsZero() {
		bucket.Created = s.now().UTC()
	}
	s.buckets[bucket.ID] = &bucketState{Bucket: bucket}
	return true, s.saveLocked(ctx)
}

// DeleteBucket forgets a bucket locally. Delivered history is permanent and
// is not affected.
func (s *Store) DeleteBucket(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.buckets[id]
	if !ok {
		return ErrNoBucket
	}
	if err := s.flushLocked(ctx, id, state); err != nil {
		return err
	}
	delete(s.buckets, id)
	return s.saveLocked(ctx)
}

func (s *Store) Buckets() map[string]Bucket {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]Bucket, len(s.buckets))
	for id, state := range s.buckets {
		result[id] = state.Bucket
	}
	return result
}

func (s *Store) Bucket(id string) (Bucket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.buckets[id]
	if !ok {
		return Bucket{}, false
	}
	return state.Bucket, true
}

// Heartbeat merges like aw-server: the same data arriving within pulsetime
// seconds of the open event's end extends it; anything else starts a new one.
func (s *Store) Heartbeat(ctx context.Context, bucketID string, heartbeat Event, pulsetime float64) (Event, error) {
	if heartbeat.Timestamp.IsZero() || heartbeat.Duration < 0 || pulsetime < 0 {
		return Event{}, ErrInvalidEvent
	}
	data, err := model.CanonicalJSON(heartbeat.Data)
	if err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	heartbeat.Data = data
	heartbeat.Timestamp = heartbeat.Timestamp.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.buckets[bucketID]
	if !ok {
		return Event{}, ErrNoBucket
	}
	s.touchLocked(state)
	open := state.Open
	if open != nil && bytes.Equal(open.Data, heartbeat.Data) && !heartbeat.Timestamp.Before(open.Timestamp) &&
		!heartbeat.Timestamp.After(open.end().Add(time.Duration(pulsetime*float64(time.Second)))) {
		if end := heartbeat.end(); end.After(open.end()) {
			open.Duration = end.Sub(open.Timestamp).Seconds()
			state.OpenDirty = true
		}
		return *open, nil
	}
	event := heartbeat
	event.ID = s.takeIDLocked()
	key := "td:" + strconv.FormatInt(event.ID, 10)
	if open != nil && heartbeat.Timestamp.Before(open.Timestamp) {
		// A late heartbeat for an earlier time is kept as its own event.
		return event, s.enqueueLocked(ctx, bucketID, key, event)
	}
	if err := s.flushLocked(ctx, bucketID, state); err != nil {
		return Event{}, err
	}
	state.Open, state.OpenKey, state.OpenDirty = &event, key, false
	if err := s.enqueueLocked(ctx, bucketID, key, event); err != nil {
		return Event{}, err
	}
	return event, s.saveLocked(ctx)
}

// Insert stores complete events. An event carrying data.event_key (as
// aw-watcher-codex does) keeps one record across re-sends and updates; others
// are identified by their content.
func (s *Store) Insert(ctx context.Context, bucketID string, events []Event) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.buckets[bucketID]
	if !ok {
		return nil, ErrNoBucket
	}
	s.touchLocked(state)
	inserted := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Timestamp.IsZero() || event.Duration < 0 {
			return nil, ErrInvalidEvent
		}
		data, err := model.CanonicalJSON(event.Data)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
		}
		event.Data, event.Timestamp = data, event.Timestamp.UTC()
		// No local id: a re-sent event must produce the same payload.
		event.ID = 0
		key := insertKey(event)
		if err := s.enqueueLocked(ctx, bucketID, key, event); err != nil {
			return nil, err
		}
		inserted = append(inserted, event)
	}
	return inserted, s.saveLocked(ctx)
}

func insertKey(event Event) string {
	var fields struct {
		EventKey string `json:"event_key"`
	}
	if json.Unmarshal(event.Data, &fields) == nil && fields.EventKey != "" {
		return "key:" + fields.EventKey
	}
	encoded, _ := json.Marshal(struct {
		Timestamp time.Time       `json:"timestamp"`
		Duration  float64         `json:"duration"`
		Data      json.RawMessage `json:"data"`
	}{event.Timestamp, event.Duration, event.Data})
	digest := sha256.Sum256(encoded)
	return "content:" + hex.EncodeToString(digest[:])
}

// Flush queues the current state of every open event that changed.
func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, state := range s.buckets {
		if err := s.flushLocked(ctx, id, state); err != nil {
			return err
		}
	}
	return s.saveLocked(ctx)
}

func (s *Store) flushLocked(ctx context.Context, bucketID string, state *bucketState) error {
	if state.Open == nil || !state.OpenDirty {
		return nil
	}
	if err := s.enqueueLocked(ctx, bucketID, state.OpenKey, *state.Open); err != nil {
		return err
	}
	state.OpenDirty = false
	return nil
}

func (s *Store) touchLocked(state *bucketState) {
	now := s.now().UTC()
	state.Bucket.LastUpdated = &now
}

func (s *Store) takeIDLocked() int64 {
	id := s.nextID
	s.nextID++
	return id
}

func (s *Store) saveLocked(ctx context.Context) error {
	now := s.now()
	encoded, err := json.Marshal(s.buckets)
	if err != nil {
		return err
	}
	if err := s.queue.SaveCursor(ctx, cursorBuckets, string(encoded), now); err != nil {
		return err
	}
	return s.queue.SaveCursor(ctx, cursorNextID, strconv.FormatInt(s.nextID, 10), now)
}

func (s *Store) enqueueLocked(ctx context.Context, bucketID, key string, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	end := event.end()
	observation := model.Observation{
		SourceInstanceKey:   "activitywatch:" + s.deviceID + ":" + bucketID,
		SourceKind:          "activitywatch",
		DeviceID:            s.deviceID,
		SourceRecordKey:     key,
		Kind:                "activitywatch.event",
		OccurredAt:          event.Timestamp,
		EndedAt:             &end,
		CollectedAt:         s.now().UTC(),
		SourceSchemaVersion: SchemaVersion,
		OriginalPayload:     payload,
	}
	if err := observation.Normalize(); err != nil {
		return err
	}
	_, err = s.queue.Enqueue(ctx, observation, s.now())
	return err
}
