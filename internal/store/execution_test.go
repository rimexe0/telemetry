package store

import (
	"encoding/json"
	"errors"
	"testing"
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
