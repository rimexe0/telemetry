package main

import (
	"testing"
	"time"
)

func TestSleepStateHoldsThroughDarkWakes(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	idle := 10 * time.Minute
	s := &sleepState{idle: func() (time.Duration, error) { return idle, nil }}
	if got := s.state(start); got != stateRunning {
		t.Fatalf("awake state = %q", got)
	}
	s.set(true, start)
	// A maintenance wake 20 minutes later: no input since before the sleep.
	idle = 30 * time.Minute
	if got := s.state(start.Add(20 * time.Minute)); got != stateSleeping {
		t.Fatalf("dark wake state = %q, want sleeping", got)
	}
	// The wake notification ends it.
	s.set(false, start.Add(9*time.Hour))
	if got := s.state(start.Add(9 * time.Hour)); got != stateRunning {
		t.Fatalf("after wake = %q", got)
	}
}

func TestSleepStateEndsOnInputWithoutWakeNotification(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	idle := 5 * time.Second
	s := &sleepState{idle: func() (time.Duration, error) { return idle, nil }}
	s.set(true, start)
	// Someone typed after the sleep began, so the Mac is in use.
	if got := s.state(start.Add(8 * time.Hour)); got != stateAwake {
		t.Fatalf("state with fresh input = %q, want awake", got)
	}
	idle = time.Hour
	if got := s.state(start.Add(9 * time.Hour)); got != stateRunning {
		t.Fatalf("the sleep was over; state = %q", got)
	}
}
