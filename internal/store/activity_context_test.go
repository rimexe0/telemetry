package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var contextNow = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func windowEvent(key, app, title, url string, start time.Time) ObservationView {
	payload, _ := json.Marshal(map[string]any{"data": map[string]any{"app": app, "title": title, "url": url}})
	end := start.Add(5 * time.Minute)
	return ObservationView{SourceInstanceKey: "activitywatch:mac:aw-watcher-window", SourceKind: "activitywatch", SourceRecordKey: key, Kind: "activitywatch.event", OccurredAt: start, EndedAt: &end, CollectedAt: start, OriginalPayload: payload}
}

func codexEvent(key, projectPath string, start time.Time) ObservationView {
	payload, _ := json.Marshal(map[string]any{"data": map[string]any{"project_path": projectPath, "chat_title": "fix build", "status": "running"}})
	end := start.Add(10 * time.Minute)
	return ObservationView{SourceInstanceKey: "activitywatch:mac:aw-watcher-codex", SourceKind: "activitywatch", SourceRecordKey: key, Kind: "activitywatch.event", OccurredAt: start, EndedAt: &end, CollectedAt: start, OriginalPayload: payload}
}

func contextFixture() (PlanBlock, []Activity, []ActivityContextRule, []SourceSummary) {
	paid := int64(1)
	block := PlanBlock{ID: 5, ActivityID: &paid, Title: "Client work", StartTime: contextNow.Add(-30 * time.Minute), DurationMinutes: 60, State: "active"}
	activities := []Activity{{ID: 1, Title: "Client work", Status: "active"}, {ID: 2, Title: "Custom ROM", Status: "active"}, {ID: 3, Title: "Inbox", Status: "active"}}
	rules := []ActivityContextRule{
		{ID: 1, ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: "/Users/ri/work/client"},
		{ID: 2, ActivityID: 2, Signal: ContextSignalPathPrefix, Pattern: "/Users/ri/rom/android_frameworks_base"},
		{ID: 3, ActivityID: 1, Signal: ContextSignalURLPrefix, Pattern: "https://github.com/client/app"},
		{ID: 4, ActivityID: 3, Signal: ContextSignalDomain, Pattern: "mail.example.com"},
	}
	last := contextNow.Add(-time.Minute)
	sources := []SourceSummary{{SourceInstanceKey: "activitywatch:mac:aw-watcher-window", SourceKind: "activitywatch", LastCollectedAt: &last}}
	return block, activities, rules, sources
}

func evaluate(observations []ObservationView, rules []ActivityContextRule, attended *ExecutionSession) AlignmentView {
	block, activities, _, sources := contextFixture()
	return EvaluateAlignment(AlignmentContext{At: contextNow, Now: contextNow, PlanDate: "2026-09-25", PlanVersionID: 1, Block: &block, Activity: &activities[0], Activities: activities, Sources: sources, Observations: observations, ContextRules: rules, AttendedSession: attended})
}

func TestSameAgentAppMapsToDifferentProjects(t *testing.T) {
	_, _, rules, _ := contextFixture()
	start := contextNow.Add(-10 * time.Minute)
	paid := evaluate([]ObservationView{windowEvent("w1", "Codex", "", "", start), codexEvent("c1", "/Users/ri/work/client/api", start)}, rules, nil)
	if paid.AlignmentState != AlignmentAligned || paid.Evidence[0].Relationship != "matched" || paid.Evidence[0].Basis != "agent_context" {
		t.Fatalf("paid Codex = %s %+v", paid.AlignmentState, paid.Evidence)
	}
	rom := evaluate([]ObservationView{windowEvent("w1", "Codex", "", "", start), codexEvent("c1", "/Users/ri/rom/android_frameworks_base", start)}, rules, nil)
	if rom.AlignmentState != AlignmentPossibleDrift || rom.Evidence[0].Relationship != "unmatched" || rom.Evidence[0].RelatedActivity != "Custom ROM" {
		t.Fatalf("ROM Codex = %s %+v", rom.AlignmentState, rom.Evidence)
	}
	// The agent's own record stays agent context, never a claim about attention.
	if rom.Evidence[1].Origin != "observed_agent_activity" || rom.Evidence[1].Relationship != "context_only" || len(rom.Evidence[1].ContextMatches) != 1 {
		t.Fatalf("agent evidence = %+v", rom.Evidence[1])
	}
}

func TestAppNameAloneNeverClassifiesDrift(t *testing.T) {
	_, _, rules, _ := contextFixture()
	start := contextNow.Add(-10 * time.Minute)
	view := evaluate([]ObservationView{windowEvent("w1", "Codex", "", "", start)}, rules, nil)
	if view.AlignmentState != AlignmentUnknown || view.Evidence[0].Relationship != "ambiguous" {
		t.Fatalf("app only = %s %+v", view.AlignmentState, view.Evidence)
	}
	// Agent work in an unmapped repository leaves the foreground ambiguous.
	view = evaluate([]ObservationView{windowEvent("w1", "Codex", "", "", start), codexEvent("c1", "/tmp/scratch", start)}, rules, nil)
	if view.AlignmentState != AlignmentUnknown {
		t.Fatalf("unknown repo = %s %+v", view.AlignmentState, view.Evidence)
	}
}

func TestConflictingContextStaysInspectable(t *testing.T) {
	_, _, rules, _ := contextFixture()
	start := contextNow.Add(-10 * time.Minute)
	view := evaluate([]ObservationView{
		windowEvent("w1", "Codex", "", "", start),
		codexEvent("c1", "/Users/ri/work/client", start),
		codexEvent("c2", "/Users/ri/rom/android_frameworks_base", start),
	}, rules, nil)
	if view.AlignmentState != AlignmentUnknown || view.Evidence[0].Relationship != "ambiguous" {
		t.Fatalf("conflict = %s %+v", view.AlignmentState, view.Evidence[0])
	}
	if len(view.Evidence[1].ContextMatches) != 1 || len(view.Evidence[2].ContextMatches) != 1 {
		t.Fatal("conflicting agent contexts must both remain visible")
	}
}

func TestBrowserContextUsesURLRulesBeforeDomains(t *testing.T) {
	_, _, rules, _ := contextFixture()
	rules = append(rules, ActivityContextRule{ID: 5, ActivityID: 2, Signal: ContextSignalDomain, Pattern: "github.com"})
	start := contextNow.Add(-10 * time.Minute)
	view := evaluate([]ObservationView{windowEvent("w1", "Chrome", "PR 12", "https://github.com/client/app/pull/12", start)}, rules, nil)
	if view.AlignmentState != AlignmentAligned || view.Evidence[0].Basis != "context_rule" {
		t.Fatalf("url prefix = %s %+v", view.AlignmentState, view.Evidence[0])
	}
	view = evaluate([]ObservationView{windowEvent("w1", "Chrome", "Inbox", "https://mail.example.com/u/0", start)}, rules, nil)
	if view.AlignmentState != AlignmentPossibleDrift || view.Evidence[0].RelatedActivity != "Inbox" {
		t.Fatalf("domain = %s %+v", view.AlignmentState, view.Evidence[0])
	}
}

func TestWaitingInterleaveContextIsDistinctFromDrift(t *testing.T) {
	_, _, rules, _ := contextFixture()
	start := contextNow.Add(-10 * time.Minute)
	primary := int64(9)
	interleave := &ExecutionSession{ID: 10, ActivityID: 3, Role: ExecutionRoleWaitingInterleave, PrimarySessionID: &primary, State: executionStateActive}
	view := evaluate([]ObservationView{windowEvent("w1", "Chrome", "Inbox", "https://mail.example.com/u/0", start)}, rules, interleave)
	if view.AlignmentState != AlignmentAligned || view.ReasonCode != AlignmentReasonUsefulInterleave || view.Evidence[0].Relationship != "interleave" {
		t.Fatalf("interleave = %s %s %+v", view.AlignmentState, view.ReasonCode, view.Evidence[0])
	}
}

func TestStaleContextDoesNotClassifyDrift(t *testing.T) {
	block, activities, rules, _ := contextFixture()
	old := contextNow.Add(-3 * time.Hour)
	sources := []SourceSummary{{SourceInstanceKey: "activitywatch:mac:aw-watcher-window", SourceKind: "activitywatch", LastCollectedAt: &old}}
	start := contextNow.Add(-10 * time.Minute)
	view := EvaluateAlignment(AlignmentContext{At: contextNow, Now: contextNow, PlanDate: "2026-09-25", PlanVersionID: 1, Block: &block, Activity: &activities[0], Activities: activities, Sources: sources,
		Observations: []ObservationView{windowEvent("w1", "Chrome", "Inbox", "https://mail.example.com/u/0", start)}, ContextRules: rules})
	if view.AlignmentState == AlignmentPossibleDrift {
		t.Fatalf("stale evidence produced drift: %+v", view)
	}
	if view.Evidence[0].SourceRecordKey != "w1" || view.Evidence[0].Context == nil || view.Evidence[0].Context.Domain != "mail.example.com" {
		t.Fatalf("provenance/context not retained: %+v", view.Evidence[0])
	}
}

func TestLongestPathPrefixWinsAndDisabledRulesAreIgnored(t *testing.T) {
	rules := []ActivityContextRule{
		{ID: 1, ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: "/work"},
		{ID: 2, ActivityID: 2, Signal: ContextSignalPathPrefix, Pattern: "/work/client"},
		{ID: 3, ActivityID: 3, Signal: ContextSignalPathPrefix, Pattern: "/work/client/app", DisabledAt: &contextNow},
	}
	matches := MatchActivityContext(ActivityContext{Paths: []string{"/work/client/app/src"}}, rules)
	if len(matches) != 1 || matches[0].ActivityID != 2 {
		t.Fatalf("matches = %+v", matches)
	}
	if got := MatchActivityContext(ActivityContext{Paths: []string{"/work/clientele"}}, rules); len(got) != 1 || got[0].ActivityID != 1 {
		t.Fatalf("prefix must respect path boundaries: %+v", got)
	}
}

func TestContextRuleValidation(t *testing.T) {
	for _, rule := range []ActivityContextRule{
		{ActivityID: 1, Signal: "app_name", Pattern: "Codex"},
		{ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: "relative/path"},
		{ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: "/"},
		{ActivityID: 1, Signal: ContextSignalDomain, Pattern: "https://github.com"},
		{ActivityID: 1, Signal: ContextSignalURLPrefix, Pattern: "https://github.com"},
		{ActivityID: 0, Signal: ContextSignalDomain, Pattern: "example.com"},
	} {
		if err := normaliseContextRule(&rule); !errors.Is(err, ErrInvalidContextRule) {
			t.Fatalf("%+v accepted: %v", rule, err)
		}
	}
	rule := ActivityContextRule{ActivityID: 1, Signal: ContextSignalPathPrefix, Pattern: " /work/client/ "}
	if err := normaliseContextRule(&rule); err != nil || rule.Pattern != "/work/client" {
		t.Fatalf("normalised = %+v, %v", rule, err)
	}
}

func TestExtractsT3AndGitContext(t *testing.T) {
	payload := json.RawMessage(`{"thread_id":"t1","project_id":"p1","branch":"main","worktree_path":"/work/client/"}`)
	context := ExtractActivityContext(ObservationView{SourceKind: "t3code", OriginalPayload: payload})
	if context.Branch != "main" || len(context.Paths) != 1 || context.Paths[0] != "/work/client" || context.WorkspaceIDs[0] != "t3:p1" {
		t.Fatalf("context = %+v", context)
	}
}
