package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	AgentRunning     = "agent_running"
	AgentCompleted   = "agent_completed"
	AgentFailed      = "agent_failed"
	AgentInterrupted = "agent_interrupted"
)

// t3TurnStatus maps the T3 Code projection_turns.state vocabulary observed
// read-only on the owner's Mac on 2026-09-25 (completed, error, interrupted,
// running). Unknown values are not interpreted.
var t3TurnStatus = map[string]string{
	"running":     AgentRunning,
	"completed":   AgentCompleted,
	"error":       AgentFailed,
	"interrupted": AgentInterrupted,
}

// AgentLifecycle is derived from one retained agent observation. The original
// observation stays authoritative; this carries its identity for provenance.
type AgentLifecycle struct {
	Tool              string    `json:"tool"`
	SessionID         string    `json:"session_id"`
	RunID             string    `json:"run_id,omitempty"`
	Status            string    `json:"status"`
	At                time.Time `json:"at"`
	SourceInstanceKey string    `json:"source_instance_key"`
	SourceRecordKey   string    `json:"source_record_key"`
}

func AgentLifecycleFromObservation(observation ObservationView) (AgentLifecycle, bool) {
	if observation.SourceKind != "t3code" || observation.Kind != "t3code.turn" {
		return AgentLifecycle{}, false
	}
	var payload map[string]any
	if err := json.Unmarshal(observation.OriginalPayload, &payload); err != nil {
		return AgentLifecycle{}, false
	}
	status, ok := t3TurnStatus[alignmentString(payload["state"])]
	threadID := alignmentString(payload["thread_id"])
	if !ok || threadID == "" {
		return AgentLifecycle{}, false
	}
	at := observation.OccurredAt
	if observation.EndedAt != nil {
		at = *observation.EndedAt
	}
	return AgentLifecycle{
		Tool: "t3code", SessionID: threadID, RunID: alignmentString(payload["turn_id"]), Status: status, At: at.UTC(),
		SourceInstanceKey: observation.SourceInstanceKey, SourceRecordKey: observation.SourceRecordKey,
	}, true
}

// AgentSessionContexts builds each T3 thread's context from retained thread
// and project observations: the thread worktree, the project's workspace root
// and the project id as workspace `t3:<id>`.
func AgentSessionContexts(observations []ObservationView) map[string]ActivityContext {
	projects := map[string]string{}
	for _, observation := range observations {
		if observation.SourceKind == "t3code" && observation.Kind == "t3code.project" {
			context := ExtractActivityContext(observation)
			if id := strings.TrimPrefix(observation.SourceRecordKey, "project:"); id != "" && len(context.Paths) > 0 {
				projects[id] = context.Paths[0]
			}
		}
	}
	threads := map[string]ActivityContext{}
	for _, observation := range observations {
		if observation.SourceKind != "t3code" || observation.Kind != "t3code.thread" {
			continue
		}
		threadID := strings.TrimPrefix(observation.SourceRecordKey, "thread:")
		context := ExtractActivityContext(observation)
		for _, workspace := range context.WorkspaceIDs {
			if root, ok := projects[strings.TrimPrefix(workspace, "t3:")]; ok && !containsString(context.Paths, root) {
				context.Paths = append(context.Paths, root)
			}
		}
		threads[threadID] = context
	}
	return threads
}

// AgentReadyCandidate is the first finished agent run tied to the waiting
// primary, with the evidence needed to trace a later return latency.
type AgentReadyCandidate struct {
	Lifecycle AgentLifecycle `json:"lifecycle"`
	Matches   []ContextMatch `json:"context_matches"`
}

// Metadata is stored on the external_ready event it produces.
func (c AgentReadyCandidate) Metadata() json.RawMessage {
	outcome := "completed"
	if c.Lifecycle.Status == AgentFailed {
		outcome = "failed"
	}
	ruleIDs := make([]int64, 0, len(c.Matches))
	for _, match := range c.Matches {
		ruleIDs = append(ruleIDs, match.RuleID)
	}
	raw, _ := json.Marshal(map[string]any{
		"agent_outcome": outcome, "agent_tool": c.Lifecycle.Tool, "agent_session_id": c.Lifecycle.SessionID, "agent_run_id": c.Lifecycle.RunID,
		"source_instance_key": c.Lifecycle.SourceInstanceKey, "source_record_key": c.Lifecycle.SourceRecordKey, "context_rule_ids": ruleIDs,
	})
	return raw
}

// FindAgentReady decides deterministically whether a finished agent run makes
// the waiting primary actionable. A run counts only when its session maps,
// through explicit owner rules, to exactly one activity and that activity is
// the primary's; it must have finished after the primary last entered
// waiting_external. Unknown or ambiguous agent sessions never qualify, and
// agents for other activities never affect this primary.
func FindAgentReady(primary *ExecutionSession, lifecycles []AgentLifecycle, contexts map[string]ActivityContext, rules []ActivityContextRule) (AgentReadyCandidate, bool) {
	if primary == nil || primary.Role != ExecutionRolePrimary || primary.EndedAt != nil || primary.State != executionStateWaitingExternal {
		return AgentReadyCandidate{}, false
	}
	waiting := latestEventOfType(primary.Events, executionEventWaitingExternal)
	if waiting == nil {
		return AgentReadyCandidate{}, false
	}
	ordered := append([]AgentLifecycle(nil), lifecycles...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].At.Before(ordered[j].At)
		}
		return ordered[i].SourceRecordKey < ordered[j].SourceRecordKey
	})
	for _, lifecycle := range ordered {
		if (lifecycle.Status != AgentCompleted && lifecycle.Status != AgentFailed) || !lifecycle.At.After(waiting.OccurredAt) {
			continue
		}
		context, known := contexts[lifecycle.SessionID]
		if !known {
			continue
		}
		matches := MatchActivityContext(context, rules)
		if ids := matchedActivities(matches); len(ids) == 1 && ids[0] == primary.ActivityID {
			return AgentReadyCandidate{Lifecycle: lifecycle, Matches: matches}, true
		}
	}
	return AgentReadyCandidate{}, false
}

func latestEventOfType(events []ExecutionEvent, eventType string) *ExecutionEvent {
	var latest *ExecutionEvent
	for index := range events {
		if events[index].EventType == eventType && (latest == nil || events[index].ID > latest.ID) {
			latest = &events[index]
		}
	}
	return latest
}
