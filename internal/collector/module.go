// Package collector hosts the desktop collector's modules. Every source the
// collector reads (focused window, browser and Codex events, T3 threads, and
// later others) is a Module; a machine runs the modules that apply to it.
package collector

import (
	"context"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
	"github.com/rimexe0/telemetry/internal/queue"
)

// Host is what modules share: the ActivityWatch-shaped event store, the
// delivery queue for their own observations, and this machine's identity.
type Host struct {
	Store    *awserver.Store
	Queue    *queue.Queue
	DeviceID string
	Hostname string
	Logf     func(format string, arguments ...any)
}

type Module interface {
	Name() string
	// Run collects until ctx ends. Returning an error stops the collector,
	// so modules report recoverable problems through their status instead.
	Run(ctx context.Context, host Host) error
	// Status is included in the collector's status record every minute.
	Status() map[string]any
}

// Health tracks a module's recent success and failure for its status.
type Health struct {
	mu          sync.Mutex
	lastSuccess time.Time
	lastError   string
	errorSince  time.Time
}

// Succeeded clears any error.
func (h *Health) Succeeded(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastSuccess, h.lastError, h.errorSince = now, "", time.Time{}
}

// Failed records an error; errorSince stays at the first failure in a row.
func (h *Health) Failed(now time.Time, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.errorSince.IsZero() {
		h.errorSince = now
	}
	h.lastError = err.Error()
}

// Fields returns the health part of a module status.
func (h *Health) Fields() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	fields := map[string]any{}
	if !h.lastSuccess.IsZero() {
		fields["last_success_at"] = h.lastSuccess.UTC()
	}
	if h.lastError != "" {
		fields["error"] = h.lastError
		fields["error_since"] = h.errorSince.UTC()
	}
	return fields
}
