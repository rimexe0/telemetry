package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNextExecutionStateUsesFixedLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		event     string
		wantState string
		terminal  bool
		wantErr   bool
	}{
		{name: "wait", state: executionStateActive, event: executionEventWaitingExternal, wantState: executionStateWaitingExternal},
		{name: "pause", state: executionStateActive, event: executionEventPaused, wantState: executionStateInterrupted},
		{name: "interrupt", state: executionStateActive, event: executionEventInterrupted, wantState: executionStateInterrupted},
		{name: "switch", state: executionStateActive, event: executionEventSwitchedOut, wantState: executionStateInterrupted},
		{name: "resume from wait", state: executionStateWaitingExternal, event: executionEventResumed, wantState: executionStateActive},
		{name: "resume from interruption", state: executionStateInterrupted, event: executionEventResumed, wantState: executionStateActive},
		{name: "complete", state: executionStateActive, event: executionEventCompleted, wantState: executionStateCompleted, terminal: true},
		{name: "abandon", state: executionStateInterrupted, event: executionEventAbandoned, wantState: executionStateAbandoned, terminal: true},
		{name: "cannot resume active", state: executionStateActive, event: executionEventResumed, wantErr: true},
		{name: "cannot wait while interrupted", state: executionStateInterrupted, event: executionEventWaitingExternal, wantErr: true},
		{name: "cannot resume completed", state: executionStateCompleted, event: executionEventResumed, wantErr: true},
		{name: "started is creation only", state: executionStateActive, event: executionEventStarted, wantErr: true},
		{name: "unknown current state", state: "unknown", event: executionEventCompleted, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, terminal, err := nextExecutionState(test.state, test.event)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if state != test.wantState || terminal != test.terminal {
				t.Fatalf("transition = (%q, %v), want (%q, %v)", state, terminal, test.wantState, test.terminal)
			}
		})
	}
}

func TestNextExecutionStateRetainsContextAndRecoveryLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		lastEvent string
		event     string
		wantState string
		wantErr   bool
	}{
		{name: "unknown next action stays active", state: executionStateActive, event: executionEventStuckUnknownNextAction, wantState: executionStateActive},
		{name: "emotional overwhelm stays active", state: executionStateActive, event: executionEventStuckEmotionalOverwhelm, wantState: executionStateActive},
		{name: "overwhelmed stays active", state: executionStateActive, event: executionEventOverwhelmed, wantState: executionStateActive},
		{name: "recovery interrupts without abandoning", state: executionStateActive, event: executionEventOfflineRecoveryStarted, wantState: executionStateInterrupted},
		{name: "recovery end retains interruption", state: executionStateInterrupted, lastEvent: executionEventOfflineRecoveryStarted, event: executionEventOfflineRecoveryEnded, wantState: executionStateInterrupted},
		{name: "resume after recovery returns active", state: executionStateInterrupted, lastEvent: executionEventOfflineRecoveryEnded, event: executionEventResumed, wantState: executionStateActive},
		{name: "external readiness keeps primary waiting", state: executionStateWaitingExternal, event: executionEventExternalReady, wantState: executionStateWaitingExternal},
		{name: "external readiness requires a wait", state: executionStateActive, event: executionEventExternalReady, wantErr: true},
		{name: "owner explanation preserves state", state: executionStateWaitingExternal, event: executionEventOwnerExplanation, wantState: executionStateWaitingExternal},
		{name: "recovery end requires a recovery start", state: executionStateInterrupted, lastEvent: executionEventInterrupted, event: executionEventOfflineRecoveryEnded, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, terminal, err := nextExecutionStateWithHistory(test.state, test.lastEvent, test.event)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if state != test.wantState || terminal {
				t.Fatalf("transition = (%q, %v), want (%q, false)", state, terminal, test.wantState)
			}
		})
	}
}

func TestExecutionMetadataRequiresBoundedObject(t *testing.T) {
	metadata, err := executionMetadata(json.RawMessage(`{"reason_category":"intentional_switch"}`))
	if err != nil || string(metadata) != `{"reason_category":"intentional_switch"}` {
		t.Fatalf("metadata = %s, %v", metadata, err)
	}
	for _, value := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`{"broken"`)} {
		if _, err := executionMetadata(value); !errors.Is(err, ErrInvalidExecutionInput) {
			t.Fatalf("metadata %s error = %v", value, err)
		}
	}
}

func TestExecutionRoleRequiresExplicitInterleaveParent(t *testing.T) {
	primary, err := executionRole(ExecutionSessionInput{ActivityID: 1})
	if err != nil || primary != ExecutionRolePrimary {
		t.Fatalf("default role = %q, %v", primary, err)
	}
	primaryID := int64(7)
	interleave, err := executionRole(ExecutionSessionInput{Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primaryID})
	if err != nil || interleave != ExecutionRoleWaitingInterleave {
		t.Fatalf("interleave role = %q, %v", interleave, err)
	}
	zeroID := int64(0)
	for _, input := range []ExecutionSessionInput{
		{Role: "unknown"},
		{Role: ExecutionRolePrimary, PrimarySessionID: &primaryID},
		{Role: ExecutionRoleWaitingInterleave},
		{Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &zeroID},
	} {
		if _, err := executionRole(input); !errors.Is(err, ErrInvalidExecutionInput) {
			t.Fatalf("invalid role input %+v error = %v", input, err)
		}
	}
}

func TestBoundedResumeMarkerTrimsAndCapsInput(t *testing.T) {
	marker := "  Resume with the first paragraph  "
	got, err := boundedResumeMarker(&marker)
	if err != nil || got == nil || *got != "Resume with the first paragraph" {
		t.Fatalf("bounded marker = %v, %v", got, err)
	}
	tooLong := strings.Repeat("x", maxPlanningTextRunes+1)
	if _, err := boundedResumeMarker(&tooLong); !errors.Is(err, ErrInvalidExecutionInput) {
		t.Fatalf("oversized marker error = %v", err)
	}
}

func TestExecutionSessionStateAtUsesRetainedEventTimes(t *testing.T) {
	start := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	waitingAt := start.Add(5 * time.Minute)
	resumedAt := start.Add(15 * time.Minute)
	completedAt := start.Add(30 * time.Minute)
	session := ExecutionSession{
		StartedAt: start,
		EndedAt:   &completedAt,
		State:     executionStateCompleted,
		Events: []ExecutionEvent{
			{EventType: executionEventStarted, OccurredAt: start},
			{EventType: executionEventWaitingExternal, OccurredAt: waitingAt},
			{EventType: executionEventResumed, OccurredAt: resumedAt},
			{EventType: executionEventCompleted, OccurredAt: completedAt},
		},
	}
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{at: start.Add(time.Minute), want: executionStateActive},
		{at: waitingAt, want: executionStateWaitingExternal},
		{at: resumedAt, want: executionStateActive},
		{at: completedAt, want: executionStateCompleted},
	} {
		got, ok := ExecutionSessionStateAt(session, test.at)
		if !ok || got != test.want {
			t.Fatalf("state at %s = %q, %v, want %q", test.at, got, ok, test.want)
		}
	}
	if _, ok := ExecutionSessionStateAt(session, start.Add(-time.Second)); ok {
		t.Fatal("session was considered active before it started")
	}
	if _, ok := ExecutionSessionStateAt(session, completedAt.Add(time.Second)); ok {
		t.Fatal("terminal session was considered open after it ended")
	}
}

func TestExecutionSessionStateAtKeepsOutOfOrderEventsUnknown(t *testing.T) {
	start := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	session := ExecutionSession{
		StartedAt: start,
		State:     executionStateActive,
		Events: []ExecutionEvent{
			{EventType: executionEventStarted, OccurredAt: start},
			{EventType: executionEventWaitingExternal, OccurredAt: start.Add(20 * time.Minute)},
			{EventType: executionEventResumed, OccurredAt: start.Add(15 * time.Minute)},
		},
	}
	state, ok := ExecutionSessionStateAt(session, start.Add(16*time.Minute))
	if !ok || state != "unknown" {
		t.Fatalf("state from out-of-order events = %q, %v, want unknown", state, ok)
	}
	state, ok = ExecutionSessionStateAt(session, start.Add(25*time.Minute))
	if !ok || state != "unknown" {
		t.Fatalf("state after out-of-order event sequence = %q, %v, want unknown", state, ok)
	}
}
