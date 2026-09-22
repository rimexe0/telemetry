package store

import "testing"

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
