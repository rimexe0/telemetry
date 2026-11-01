package main

import (
	"sync"
	"time"
)

// sleepState remembers an announced sleep until the computer really wakes.
// A sleeping Mac wakes in the dark every few minutes for maintenance without
// telling apps; the collector runs then, and a minute's "running" status would
// undo the announcement, so the server would take the silence that follows for
// a stopped collector.
type sleepState struct {
	mu    sync.Mutex
	since time.Time // zero while awake
	idle  func() (time.Duration, error)
}

func (s *sleepState) set(sleeping bool, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sleeping {
		s.since = now
	} else {
		s.since = time.Time{}
	}
}

// state is what the minute's status reports: "sleeping" from the sleep
// announcement until the wake notification, or until input newer than the
// sleep shows the computer is in use even if the notification was missed;
// that is reported as "awake", since the server holds the sleep until then.
func (s *sleepState) state(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since.IsZero() {
		return stateRunning
	}
	if s.idle != nil {
		if idle, err := s.idle(); err == nil && idle < now.Sub(s.since) {
			s.since = time.Time{}
			return stateAwake
		}
	}
	return stateSleeping
}
