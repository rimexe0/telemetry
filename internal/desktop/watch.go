// Package desktop samples the foreground window and input idleness and turns
// them into ActivityWatch-shaped heartbeats, with the same bucket names,
// timing and data as aw-watcher-window and aw-watcher-afk. Reading the
// system is behind Platform so other operating systems only add that part.
package desktop

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
)

// Window is the focused application and its focused window.
type Window struct {
	App      string
	BundleID string
	Title    string
	// PID identifies the app's process, for hiding it when it is blocked.
	PID int
}

type Platform interface {
	// ActiveWindow returns ok=false when nothing is focused.
	ActiveWindow() (window Window, ok bool, err error)
	// IdleTime is the time since the last keyboard or pointer input.
	IdleTime() (time.Duration, error)
	ScreenLocked() (bool, error)
	// CanReadTitles reports whether window titles are readable; prompt asks
	// the operating system to request the permission.
	CanReadTitles(prompt bool) bool
}

// Health is what the watchers report about themselves, for the collector's
// status record. It is safe for concurrent use.
type Health struct {
	mu            sync.Mutex
	titles        bool
	lastWindow    time.Time
	lastError     string
	lastErrorTime time.Time
}

type HealthSnapshot struct {
	TitlesReadable bool       `json:"titles_readable"`
	LastWindowAt   *time.Time `json:"last_window_sample_at,omitempty"`
	LastError      string     `json:"last_sampling_error,omitempty"`
	LastErrorAt    *time.Time `json:"last_sampling_error_at,omitempty"`
}

func (h *Health) Snapshot() HealthSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot := HealthSnapshot{TitlesReadable: h.titles, LastError: h.lastError}
	if !h.lastWindow.IsZero() {
		at := h.lastWindow.UTC()
		snapshot.LastWindowAt = &at
	}
	if !h.lastErrorTime.IsZero() {
		at := h.lastErrorTime.UTC()
		snapshot.LastErrorAt = &at
	}
	return snapshot
}

func (h *Health) record(titles bool, windowOK bool, err error, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles = titles
	if windowOK {
		h.lastWindow = now
	}
	if err != nil {
		h.lastError, h.lastErrorTime = err.Error(), now
	}
}

type Options struct {
	Health       *Health
	Hostname     string
	WindowPoll   time.Duration
	AFKPoll      time.Duration
	AFKTimeout   time.Duration
	Now          func() time.Time
	Logf         func(format string, arguments ...any)
	WindowBucket string
	AFKBucket    string
}

func (o *Options) defaults() {
	if o.WindowPoll == 0 {
		o.WindowPoll = time.Second
	}
	if o.AFKPoll == 0 {
		o.AFKPoll = 5 * time.Second
	}
	if o.AFKTimeout == 0 {
		o.AFKTimeout = 3 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Health == nil {
		o.Health = &Health{}
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	o.WindowBucket = "aw-watcher-window_" + o.Hostname
	o.AFKBucket = "aw-watcher-afk_" + o.Hostname
}

// Run watches until ctx ends.
func Run(ctx context.Context, store *awserver.Store, platform Platform, options Options) error {
	options.defaults()
	for _, bucket := range []awserver.Bucket{
		{ID: options.WindowBucket, Type: "currentwindow", Client: "telemetry-desktop", Hostname: options.Hostname},
		{ID: options.AFKBucket, Type: "afkstatus", Client: "telemetry-desktop", Hostname: options.Hostname},
	} {
		if _, err := store.EnsureBucket(ctx, bucket); err != nil {
			return err
		}
	}
	window := time.NewTicker(options.WindowPoll)
	defer window.Stop()
	afkTicker := time.NewTicker(options.AFKPoll)
	defer afkTicker.Stop()
	afk := &afkWatcher{store: store, platform: platform, options: options}
	// Asking once per start shows the operating system's permission prompt.
	titles := platform.CanReadTitles(true)
	if !titles {
		options.Logf("window titles are not readable yet; grant Accessibility access to telemetry-desktop")
	}
	var lastError string
	report := func(err error) {
		if err != nil && err.Error() != lastError {
			options.Logf("desktop sampling: %v", err)
		}
		if err == nil {
			lastError = ""
		} else {
			lastError = err.Error()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-window.C:
			sampled, err := sampleWindow(ctx, store, platform, options)
			report(err)
			if !titles && platform.CanReadTitles(false) {
				titles = true
				options.Logf("window titles are readable now")
			}
			options.Health.record(titles, sampled, err, options.Now())
		case <-afkTicker.C:
			err := afk.sample(ctx)
			report(err)
			if err != nil {
				options.Health.record(titles, false, err, options.Now())
			}
		}
	}
}

// sampleWindow reports whether a window was recorded.
func sampleWindow(ctx context.Context, store *awserver.Store, platform Platform, options Options) (bool, error) {
	window, ok, err := platform.ActiveWindow()
	if err != nil || !ok {
		return false, err
	}
	data := map[string]string{"app": window.App, "title": window.Title}
	if window.BundleID != "" {
		data["bundle_id"] = window.BundleID
	}
	err = heartbeat(ctx, store, options.WindowBucket, options.Now(), 0, data, (options.WindowPoll + time.Second).Seconds())
	return err == nil, err
}

// afkWatcher follows aw-watcher-afk: heartbeats are dated at the last input,
// and an afk heartbeat lasts until now, so active and afk spans never
// overlap. A locked screen counts as afk immediately.
type afkWatcher struct {
	store    *awserver.Store
	platform Platform
	options  Options
	afk      bool
	// since is when the current afk span began.
	since time.Time
}

func (w *afkWatcher) sample(ctx context.Context) error {
	idle, err := w.platform.IdleTime()
	if err != nil {
		return err
	}
	locked, err := w.platform.ScreenLocked()
	if err != nil {
		return err
	}
	now := w.options.Now()
	lastInput := now.Add(-idle)
	// Like aw-watcher-afk: inputs less than the AFK timeout apart belong to
	// one active span.
	pulse := (w.options.AFKTimeout + w.options.AFKPoll).Seconds()
	nowAFK := locked || idle >= w.options.AFKTimeout
	if nowAFK != w.afk {
		if err := w.beat(ctx, lastInput, now, pulse); err != nil {
			return err
		}
		w.afk = nowAFK
		w.since = lastInput.Add(time.Millisecond)
		lastInput = w.since
	}
	return w.beat(ctx, lastInput, now, pulse)
}

func (w *afkWatcher) beat(ctx context.Context, lastInput, now time.Time, pulse float64) error {
	if w.afk {
		return heartbeat(ctx, w.store, w.options.AFKBucket, w.since, max(now.Sub(w.since), 0), status(true), pulse)
	}
	return heartbeat(ctx, w.store, w.options.AFKBucket, lastInput, 0, status(false), pulse)
}

func status(afk bool) map[string]string {
	if afk {
		return map[string]string{"status": "afk"}
	}
	return map[string]string{"status": "not-afk"}
}

func heartbeat(ctx context.Context, store *awserver.Store, bucket string, at time.Time, duration time.Duration, data any, pulsetime float64) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = store.Heartbeat(ctx, bucket, awserver.Event{Timestamp: at, Duration: duration.Seconds(), Data: encoded}, pulsetime)
	return err
}
