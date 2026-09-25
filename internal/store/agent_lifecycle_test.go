package store

import (
	"encoding/json"
	"testing"
	"time"
)

var agentWaitAt = time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

func t3Observation(kind, key string, at time.Time, ended *time.Time, payload map[string]any) ObservationView {
	raw, _ := json.Marshal(payload)
	return ObservationView{SourceInstanceKey: "t3code:mac:state", SourceKind: "t3code", SourceRecordKey: key, Kind: kind, OccurredAt: at, EndedAt: ended, OriginalPayload: raw}
}

func turn(thread, turnID, state string, at time.Time) ObservationView {
	ended := at
	return t3Observation("t3code.turn", "turn:"+thread+":id:"+turnID, at.Add(-time.Minute), &ended, map[string]any{"thread_id": thread, "turn_id": turnID, "state": state})
}

func waitingPrimary() *ExecutionSession {
	return &ExecutionSession{ID: 7, ActivityID: 1, Role: ExecutionRolePrimary, State: executionStateWaitingExternal, Events: []ExecutionEvent{
		{ID: 1, EventType: executionEventStarted, OccurredAt: agentWaitAt.Add(-time.Hour)},
		{ID: 2, EventType: executionEventWaitingExternal, OccurredAt: agentWaitAt},
	}}
}

func agentFixture() ([]ObservationView, []ActivityContextRule) {
	observations := []ObservationView{
		t3Observation("t3code.project", "project:p-client", agentWaitAt.Add(-48*time.Hour), nil, map[string]any{"project_id": "p-client", "workspace_root": "/work/client"}),
		t3Observation("t3code.thread", "thread:t-client", agentWaitAt.Add(-time.Hour), nil, map[string]any{"thread_id": "t-client", "project_id": "p-client"}),
		t3Observation("t3code.thread", "thread:t-rom", agentWaitAt.Add(-time.Hour), nil, map[string]any{"thread_id": "t-rom", "project_id": "p-rom", "worktree_path": "/rom/frameworks"}),
		t3Observation("t3code.thread", "thread:t-scratch", agentWaitAt.Add(-time.Hour), nil, map[string]any{"thread_id": "t-scratch", "worktree_path": "/tmp/scratch"}),
	}
	rules := []ActivityContextRule{
		{ID: 1, ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: "/work/client"},
		{ID: 2, ActivityID: 2, Signal: ContextSignalPathPrefix, Pattern: "/rom"},
	}
	return observations, rules
}

func lifecyclesOf(observations []ObservationView) []AgentLifecycle {
	result := []AgentLifecycle{}
	for _, observation := range observations {
		if lifecycle, ok := AgentLifecycleFromObservation(observation); ok {
			result = append(result, lifecycle)
		}
	}
	return result
}

func TestAgentReadyLinksOnlyThroughExplicitProjectRules(t *testing.T) {
	context, rules := agentFixture()
	observations := append(context,
		turn("t-scratch", "a", "completed", agentWaitAt.Add(5*time.Minute)), // unknown project
		turn("t-rom", "b", "completed", agentWaitAt.Add(6*time.Minute)),     // other activity's agent
		turn("t-client", "c", "running", agentWaitAt.Add(7*time.Minute)),
		turn("t-client", "d", "completed", agentWaitAt.Add(9*time.Minute)),
	)
	candidate, ok := FindAgentReady(waitingPrimary(), lifecyclesOf(observations), AgentSessionContexts(observations), rules)
	if !ok || candidate.Lifecycle.RunID != "d" || candidate.Lifecycle.SourceRecordKey != "turn:t-client:id:d" {
		t.Fatalf("candidate = %+v, %v", candidate, ok)
	}
	var metadata map[string]any
	if err := json.Unmarshal(candidate.Metadata(), &metadata); err != nil || metadata["agent_outcome"] != "completed" || metadata["source_record_key"] != "turn:t-client:id:d" {
		t.Fatalf("metadata = %s", candidate.Metadata())
	}
}

func TestUnknownOrParallelAgentsNeverQualify(t *testing.T) {
	context, rules := agentFixture()
	observations := append(context, turn("t-scratch", "a", "completed", agentWaitAt.Add(5*time.Minute)), turn("t-rom", "b", "completed", agentWaitAt.Add(6*time.Minute)), turn("t-missing", "c", "completed", agentWaitAt.Add(7*time.Minute)))
	if candidate, ok := FindAgentReady(waitingPrimary(), lifecyclesOf(observations), AgentSessionContexts(observations), rules); ok {
		t.Fatalf("unrelated agent qualified: %+v", candidate)
	}
	// Ambiguous thread context (maps to two activities) does not qualify either.
	rules = append(rules, ActivityContextRule{ID: 3, ActivityID: 2, Signal: ContextSignalWorkspaceID, Pattern: "t3:p-client"})
	observations = append(context, turn("t-client", "d", "completed", agentWaitAt.Add(9*time.Minute)))
	if _, ok := FindAgentReady(waitingPrimary(), lifecyclesOf(observations), AgentSessionContexts(observations), rules); ok {
		t.Fatal("ambiguous agent context qualified")
	}
}

func TestAgentReadyRequiresFinishAfterWaitingAndAWaitingPrimary(t *testing.T) {
	context, rules := agentFixture()
	before := append(context, turn("t-client", "old", "completed", agentWaitAt.Add(-time.Minute)), turn("t-client", "x", "interrupted", agentWaitAt.Add(time.Minute)))
	if _, ok := FindAgentReady(waitingPrimary(), lifecyclesOf(before), AgentSessionContexts(before), rules); ok {
		t.Fatal("run finished before waiting, or interrupted run, qualified")
	}
	after := append(context, turn("t-client", "d", "completed", agentWaitAt.Add(time.Minute)))
	active := waitingPrimary()
	active.State = executionStateActive
	if _, ok := FindAgentReady(active, lifecyclesOf(after), AgentSessionContexts(after), rules); ok {
		t.Fatal("active primary received agent readiness")
	}
}

func TestFailedAgentIsDistinctFromCompleted(t *testing.T) {
	context, rules := agentFixture()
	observations := append(context, turn("t-client", "e", "error", agentWaitAt.Add(2*time.Minute)))
	candidate, ok := FindAgentReady(waitingPrimary(), lifecyclesOf(observations), AgentSessionContexts(observations), rules)
	var metadata map[string]any
	_ = json.Unmarshal(candidate.Metadata(), &metadata)
	if !ok || candidate.Lifecycle.Status != AgentFailed || metadata["agent_outcome"] != "failed" {
		t.Fatalf("failed agent = %+v %v", candidate, metadata)
	}
	if _, ok := AgentLifecycleFromObservation(turn("t-client", "z", "mystery", agentWaitAt)); ok {
		t.Fatal("unknown T3 state was interpreted")
	}
}
