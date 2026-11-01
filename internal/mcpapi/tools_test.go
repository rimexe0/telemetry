package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rimexe0/telemetry/internal/activity"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/store"
)

// toolsRepository returns T3 records and remembers applied label changes.
type toolsRepository struct {
	fakeRepository
	coding  []activity.Record
	applied []activity.LabelChange
}

func (r *toolsRepository) CodingRecords(context.Context, time.Time) ([]activity.Record, error) {
	return r.coding, nil
}

func (r *toolsRepository) ApplyLabelChanges(_ context.Context, changes []activity.LabelChange, reason string) (store.AgentChange, error) {
	for i := range changes {
		if err := changes[i].Normalize(); err != nil {
			return store.AgentChange{}, err
		}
	}
	if strings.TrimSpace(reason) == "" {
		return store.AgentChange{}, errors.New("reason is required")
	}
	r.applied = append(r.applied, changes...)
	encoded, _ := json.Marshal(changes)
	previous, _ := json.Marshal([]activity.LabelChange{{Action: "remove", Kind: changes[0].Kind, Match: changes[0].Match}})
	return store.AgentChange{ID: 1, Kind: store.AgentChangeLabels, Changes: encoded, Previous: previous, Reason: reason}, nil
}

// Real stored changes carry JSON objects; the tool output must validate.
func (r *toolsRepository) ListAgentChanges(context.Context, int) ([]store.AgentChange, error) {
	return []store.AgentChange{{ID: 1, Kind: store.AgentChangeLabels, Reason: "x",
		Changes:  json.RawMessage(`[{"action":"set","kind":"site","match":"x.com","label":"distraction"}]`),
		Previous: json.RawMessage(`[{"action":"remove","kind":"site","match":"x.com"}]`)}}, nil
}

func connect(t *testing.T, repository store.Repository, now time.Time) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry-test", Version: "0"}, nil)
	addActivityTools(server, repository, func() time.Time { return now })
	addChangeTools(server, repository)
	addAnalysisTools(server, repository, func() time.Time { return now })
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestToolsAreRegisteredWithoutProposalTools(t *testing.T) {
	session := connect(t, &toolsRepository{}, time.Now())
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"activity_now", "activity_summary", "activity_timeline", "coding_threads", "collection_incidents",
		"activity_labels", "blocklist", "change_activity_labels", "recent_changes", "revert_change"} {
		if !names[want] {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}
	for _, gone := range []string{"propose_activity_labels", "propose_blocklist_change", "change_blocklist"} {
		if names[gone] {
			t.Fatalf("%s should no longer exist", gone)
		}
	}
}

func TestCodingThreadsToolReturnsActiveThreads(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	record := func(kind, payload string) activity.Record {
		return activity.Record{Source: "t3:mac-1:env", Device: "mac-1", Kind: kind, Start: now, Payload: json.RawMessage(payload)}
	}
	repository := &toolsRepository{coding: []activity.Record{
		record("t3.thread", `{"thread_id":"a","title":"Fix collector","updated_at":"2026-09-30T11:00:00Z","pending_user_input_count":1}`),
		record("t3.turn", `{"thread_id":"a","state":"completed","requested_at":"2026-09-30T10:00:00Z","started_at":"2026-09-30T10:00:00Z","completed_at":"2026-09-30T10:30:00Z"}`),
	}}
	session := connect(t, repository, now)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "coding_threads", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("call = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output CodingThreadsOutput
	json.Unmarshal(encoded, &output)
	if len(output.Threads) != 1 || output.Threads[0].MessagesSent != 1 || !output.Threads[0].WaitingForOwner || output.Threads[0].AgentMinutes != 30 {
		t.Fatalf("output = %+v", output)
	}
}

func TestChangeLabelsToolAppliesAndRejectsBadInput(t *testing.T) {
	repository := &toolsRepository{}
	session := connect(t, repository, time.Now())
	ok, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_activity_labels", Arguments: map[string]any{
		"changes": []map[string]any{{"action": "set", "kind": "site", "match": "https://www.x.com/home", "label": "distraction"}},
		"reason":  "owner approved in chat",
	}})
	if err != nil || ok.IsError || len(repository.applied) != 1 || repository.applied[0].Match != "x.com" {
		t.Fatalf("apply = %+v, %v, applied %+v", ok, err, repository.applied)
	}
	bad, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_activity_labels", Arguments: map[string]any{
		"changes": []map[string]any{{"action": "set", "kind": "planet", "match": "x", "label": "y"}},
		"reason":  "test",
	}})
	if err != nil || !bad.IsError {
		t.Fatalf("invalid kind must be a tool error: %+v, %v", bad, err)
	}
}

func TestRecentChangesOutputValidates(t *testing.T) {
	session := connect(t, &toolsRepository{}, time.Now())
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "recent_changes", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("recent_changes = %+v, %v", result, err)
	}
}

type planningRepository struct {
	toolsRepository
	date     time.Time
	plan     store.PlanInput
	conflict error
	stored   *store.Plan
	records  []activity.Record

	followUps []store.FollowUpChange
	rules     []store.TaskRuleChange
	schedule  store.ScheduleChange
	opening   store.Opening
	labels    []store.ActivityLabel
}

func (r *planningRepository) ListActivityLabels(context.Context) ([]store.ActivityLabel, error) {
	return r.labels, nil
}

func (r *planningRepository) ApplyDayPlan(_ context.Context, date time.Time, input store.PlanInput, _ string) (store.AgentChange, store.Plan, error) {
	r.date, r.plan = date, input
	blocks := []store.PlanBlock{}
	for i, spec := range input.Blocks {
		blocks = append(blocks, store.PlanBlock{ID: int64(i + 1), ActivityID: spec.ActivityID, Title: spec.Title, StartTime: spec.StartTime, DurationMinutes: spec.DurationMinutes, State: "planned"})
	}
	return store.AgentChange{ID: 3, Kind: store.AgentChangePlan, Changes: json.RawMessage(`{}`), Previous: json.RawMessage(`{}`)},
		store.Plan{PlanDate: date.Format("2006-01-02"), VersionID: 9, Blocks: blocks}, nil
}

func (r *planningRepository) AddPlanBlock(_ context.Context, date time.Time, spec store.PlanBlockSpec, _ string) (store.AgentChange, store.Plan, error) {
	if r.conflict != nil {
		return store.AgentChange{}, store.Plan{}, r.conflict
	}
	r.date, r.plan = date, store.PlanInput{Blocks: []store.PlanBlockSpec{spec}}
	return store.AgentChange{ID: 5, Kind: store.AgentChangePlan, Changes: json.RawMessage(`{}`), Previous: json.RawMessage(`{}`)},
		store.Plan{PlanDate: date.Format("2006-01-02"), VersionID: 10, Blocks: []store.PlanBlock{{ID: 7, Title: spec.Title, StartTime: spec.StartTime,
			DurationMinutes: spec.DurationMinutes, Focus: spec.Focus, State: "planned"}}}, nil
}

func TestBlockNowAddsAFocusBlockStartingNow(t *testing.T) {
	repository := &planningRepository{}
	// 22:30:40 UTC on Sep 30 is 01:30 on Oct 1 in Istanbul.
	session := connectPlanning(t, repository, time.Date(2026, 9, 30, 22, 30, 40, 0, time.UTC))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "block_now", Arguments: map[string]any{
		"minutes": 30, "reason": "block for 30 minutes"}})
	if err != nil || result.IsError {
		t.Fatalf("block_now = %+v, %v", result, err)
	}
	spec := repository.plan.Blocks[0]
	if repository.date.Format("2006-01-02") != "2026-10-01" || !spec.Focus || spec.ActivityID != nil || spec.Title != "Focus" ||
		!spec.StartTime.Equal(time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC)) || spec.DurationMinutes != 30 {
		t.Fatalf("date %s spec %+v", repository.date, spec)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output BlockNowOutput
	json.Unmarshal(encoded, &output)
	if output.Change.ID != 5 || output.Block.Start != "01:30" || output.Block.End != "02:00" || !output.Block.Closes {
		t.Fatalf("output = %+v", output)
	}

	repository.conflict = &store.PlanConflictError{Title: "Dentist", Start: time.Date(2026, 9, 30, 22, 45, 0, 0, time.UTC), End: time.Date(2026, 9, 30, 23, 15, 0, 0, time.UTC)}
	refused, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "block_now", Arguments: map[string]any{"minutes": 30, "reason": "x"}})
	text, _ := json.Marshal(refused.Content)
	if !refused.IsError || !strings.Contains(string(text), "Dentist") || !strings.Contains(string(text), "01:45 to 02:15") {
		t.Fatalf("an overlap must be refused with the block's local time: %s", text)
	}
}

func (r *planningRepository) LatestPlan(context.Context, time.Time) (*store.Plan, error) {
	return r.stored, nil
}

func (r *planningRepository) ActivityRecords(context.Context, time.Time, time.Time) ([]activity.Record, map[string]time.Time, error) {
	return r.records, nil, nil
}

func TestPlanReviewSetsRecordedActivityAgainstBlocks(t *testing.T) {
	// 09:00 UTC is 12:00 in Istanbul; the day starts at 21:00 UTC the day before.
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 1, hour, minute, 0, 0, time.UTC) }
	end := at(8, 30)
	repository := &planningRepository{
		stored: &store.Plan{VersionID: 2, Blocks: []store.PlanBlock{
			{ID: 1, Title: "Focus", StartTime: at(8, 0), DurationMinutes: 45, Focus: true, State: "planned"},
			{ID: 2, Title: "Dinner", StartTime: at(16, 0), DurationMinutes: 30, State: "planned"},
		}},
		records: []activity.Record{{Source: "android:phone:usage", Device: "phone", Kind: "usage", Start: at(8, 10), End: &end,
			Payload: json.RawMessage(`{"package_name":"com.twitter.android","event_type":"activity_resumed"}`)}},
	}
	session := connectPlanning(t, repository, at(9, 0))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "plan_review", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("plan_review = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output PlanReviewOutput
	json.Unmarshal(encoded, &output)
	if !output.PlanExists || len(output.Blocks) != 2 || output.Blocks[0].Status != "past" || !output.Blocks[0].Closes ||
		output.Blocks[0].Observed == nil || output.Blocks[0].Observed.Minutes != 45 || output.Blocks[1].Status != "upcoming" || output.Blocks[1].Observed != nil {
		t.Fatalf("output = %s", encoded)
	}
	if output.Outside.Minutes != 12*60-45 || len(output.Insights) == 0 {
		t.Fatalf("outside = %+v insights = %+v", output.Outside, output.Insights)
	}
}

func (r *planningRepository) ApplyFollowUpChanges(_ context.Context, changes []store.FollowUpChange, _ string) (store.AgentChange, []store.FollowUp, error) {
	r.followUps = changes
	written := []store.FollowUp{}
	for i, change := range changes {
		written = append(written, store.FollowUp{ID: int64(i + 1), Title: *change.Title, DueAt: *change.DueAt, NagMinutes: change.NagMinutes, State: store.FollowUpOpen})
	}
	return store.AgentChange{ID: 8, Kind: store.AgentChangeFollowUps, Changes: json.RawMessage(`[]`), Previous: json.RawMessage(`[]`)}, written, nil
}

func TestFollowUpsResolveLocalTimes(t *testing.T) {
	repository := &planningRepository{}
	// 15:10 UTC is 18:10 in Istanbul.
	session := connectPlanning(t, repository, time.Date(2026, 10, 1, 15, 10, 0, 0, time.UTC))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_follow_ups", Arguments: map[string]any{
		"changes": []map[string]any{
			{"action": "add", "title": "Hang the laundry", "in_minutes": 60, "nag_minutes": 10},
			{"action": "add", "title": "Call the bank", "at": "09:00"},
			{"action": "add", "title": "Take out the bread", "at": "18:40"},
		}, "reason": "washing machine, 1 hour"}})
	if err != nil || result.IsError {
		t.Fatalf("change_follow_ups = %+v, %v", result, err)
	}
	due := func(i int) time.Time { return *repository.followUps[i].DueAt }
	// 09:00 has passed today, so it means tomorrow; 18:40 is still today.
	if !due(0).Equal(time.Date(2026, 10, 1, 16, 10, 0, 0, time.UTC)) || !due(1).Equal(time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)) ||
		!due(2).Equal(time.Date(2026, 10, 1, 15, 40, 0, 0, time.UTC)) || *repository.followUps[0].NagMinutes != 10 {
		t.Fatalf("due = %v, %v, %v", due(0), due(1), due(2))
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output ChangeFollowUpsOutput
	json.Unmarshal(encoded, &output)
	if output.Change.ID != 8 || output.FollowUps[0].Due != "2026-10-01 19:10" || output.FollowUps[0].DueInMinutes != 60 {
		t.Fatalf("output = %s", encoded)
	}
	bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_follow_ups", Arguments: map[string]any{
		"changes": []map[string]any{{"action": "add", "title": "No time"}}, "reason": "x"}})
	if !bad.IsError {
		t.Fatal("a new follow-up without a time must be refused")
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "follow_ups", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("follow_ups = %+v, %v", listed, err)
	}
}

func (r *planningRepository) ApplyTaskRuleChanges(_ context.Context, changes []store.TaskRuleChange, _ string) (store.AgentChange, []store.ActivityContextRule, error) {
	r.rules = changes
	written := []store.ActivityContextRule{}
	for i, change := range changes {
		written = append(written, store.ActivityContextRule{ID: int64(i + 1), ActivityID: change.TaskID, Signal: change.Signal, Pattern: change.Pattern})
	}
	return store.AgentChange{ID: 9, Kind: store.AgentChangeRules, Changes: json.RawMessage(`[]`), Previous: json.RawMessage(`[]`)}, written, nil
}

func TestChangeTaskRulesPassesRulesThrough(t *testing.T) {
	repository := &planningRepository{}
	session := connectPlanning(t, repository, time.Now())
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "change_task_rules", Arguments: map[string]any{
		"changes": []map[string]any{
			{"action": "add", "task_id": 7, "signal": "app", "pattern": "T3 Code (Nightly)"},
			{"action": "add", "task_id": 7, "signal": "domain", "pattern": "github.com"},
		}, "reason": "working on Telemetry in T3 Code today"}})
	if err != nil || result.IsError {
		t.Fatalf("change_task_rules = %+v, %v", result, err)
	}
	if len(repository.rules) != 2 || repository.rules[0].TaskID != 7 || repository.rules[0].Signal != "app" || repository.rules[1].Pattern != "github.com" {
		t.Fatalf("rules = %+v", repository.rules)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output ChangeTaskRulesOutput
	json.Unmarshal(encoded, &output)
	if output.Change.ID != 9 || len(output.Rules) != 2 || output.Rules[0].TaskID != 7 {
		t.Fatalf("output = %s", encoded)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "task_rules", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("task_rules = %+v, %v", listed, err)
	}
}

func (r *planningRepository) ApplyBlockingSchedule(_ context.Context, change store.ScheduleChange, _ string) (store.AgentChange, policy.Config, error) {
	r.schedule = change
	config := policy.Config{ClosedByDefault: change.ClosedByDefault != nil && *change.ClosedByDefault, OpenAddresses: change.AddOpenAddresses}
	config.PlanGate.FromWake = change.LockUntilPlanned != nil && *change.LockUntilPlanned
	if change.DistractionTimes != nil {
		config.DistractionTimes = *change.DistractionTimes
	}
	return store.AgentChange{ID: 11, Kind: store.AgentChangeSchedule, Changes: json.RawMessage(`{}`), Previous: json.RawMessage(`{}`)}, config, nil
}

func (r *planningRepository) OpenNow(_ context.Context, opening store.Opening, _ string) (store.AgentChange, store.Opening, error) {
	r.opening = opening
	opening.ID = 4
	return store.AgentChange{ID: 12, Kind: store.AgentChangeOpening, Changes: json.RawMessage(`{}`), Previous: json.RawMessage(`{}`)}, opening, nil
}

func TestScheduleToolsSetTimesAndOpenForAPurpose(t *testing.T) {
	repository := &planningRepository{}
	// 08:00 UTC is 11:00 in Istanbul.
	session := connectPlanning(t, repository, time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_blocking_schedule", Arguments: map[string]any{
		"closed_by_default": true, "distraction_times": []map[string]any{{"start": "20:00", "end": "22:00"}},
		"add_open_addresses": []string{"https://www.youtube.com/watch?v=rFZHOHl-L8A"}, "reason": "block by default, distraction time 8 to 10"}})
	if err != nil || result.IsError {
		t.Fatalf("set_blocking_schedule = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var schedule SetBlockingScheduleOutput
	json.Unmarshal(encoded, &schedule)
	if !*repository.schedule.ClosedByDefault || (*repository.schedule.DistractionTimes)[0].End != "22:00" || !schedule.Schedule.ClosedByDefault ||
		schedule.Schedule.DistractionTimes[0].Start != "20:00" || schedule.Schedule.OpenAddresses[0] != "youtube.com/watch?v=rFZHOHl-L8A" {
		t.Fatalf("schedule = %s", encoded)
	}
	empty, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_blocking_schedule", Arguments: map[string]any{"reason": "x"}})
	if !empty.IsError {
		t.Fatal("a change with nothing in it must be refused")
	}
	opened, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "open_now", Arguments: map[string]any{
		"minutes": 20, "sites": []string{"youtube.com"}, "purpose": "a tutorial for the album work"}})
	if err != nil || opened.IsError {
		t.Fatalf("open_now = %+v, %v", opened, err)
	}
	encoded, _ = json.Marshal(opened.StructuredContent)
	var output OpenNowOutput
	json.Unmarshal(encoded, &output)
	if !repository.opening.Until.Equal(time.Date(2026, 10, 2, 8, 20, 0, 0, time.UTC)) || output.Opening.Until != "11:20" || output.Opening.ID != 4 {
		t.Fatalf("opening = %s", encoded)
	}
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "blocking_schedule", Arguments: map[string]any{}})
	if err != nil || listed.IsError {
		t.Fatalf("blocking_schedule = %+v, %v", listed, err)
	}
}

func TestLockUntilPlannedNeedsAWayToReachTheAssistant(t *testing.T) {
	repository := &planningRepository{}
	session := connectPlanning(t, repository, time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC))
	lock := map[string]any{"lock_until_planned": true, "reason": "the systems should be locked until i plan the day out"}
	refused, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_blocking_schedule", Arguments: lock})
	if !refused.IsError || repository.schedule.LockUntilPlanned != nil {
		t.Fatal("with nothing labelled essential the owner could not reach the assistant, so the lock must be refused")
	}
	repository.labels = []store.ActivityLabel{{Kind: "app", Match: "tw.nekomimi.nekogram", Label: "essential"}}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_blocking_schedule", Arguments: lock})
	if err != nil || result.IsError || repository.schedule.LockUntilPlanned == nil || !*repository.schedule.LockUntilPlanned {
		t.Fatalf("set_blocking_schedule = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var schedule SetBlockingScheduleOutput
	json.Unmarshal(encoded, &schedule)
	if !schedule.Schedule.LockUntilPlanned || len(schedule.Schedule.Essentials) != 1 || schedule.Schedule.Essentials[0] != "tw.nekomimi.nekogram" {
		t.Fatalf("schedule = %s", encoded)
	}
}

func TestOutreachLogShowsWhatWasSentInLocalTime(t *testing.T) {
	session := connectPlanning(t, &planningRepository{}, time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "outreach_log", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("outreach_log = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output OutreachLogOutput
	json.Unmarshal(encoded, &output)
	if len(output.Messages) != 2 || output.Messages[1].At != "2026-10-02 08:28" || output.Messages[1].Message != "You woke at 08:26." || output.Messages[0].Message != "" {
		t.Fatalf("log = %s", encoded)
	}
	if bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "outreach_log", Arguments: map[string]any{"hours": 999}}); !bad.IsError {
		t.Fatal("too many hours must be refused")
	}
}

func TestTaskPatternsToolValidates(t *testing.T) {
	session := connectPlanning(t, &planningRepository{}, time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "task_patterns", Arguments: map[string]any{"days": 3}})
	if err != nil || result.IsError {
		t.Fatalf("task_patterns = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output TaskPatternsOutput
	json.Unmarshal(encoded, &output)
	if output.From != "2026-09-30" || output.To != "2026-10-02" {
		t.Fatalf("range = %s", encoded)
	}
	if bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "task_patterns", Arguments: map[string]any{"days": 60}}); !bad.IsError {
		t.Fatal("too many days must be refused")
	}
}

func TestFocusAndMemoryToolsValidate(t *testing.T) {
	session := connectPlanning(t, &planningRepository{}, time.Now())
	for name, arguments := range map[string]map[string]any{
		"focus_session":     {},
		"task_memory":       {},
		"remember_for_task": {"task_id": 4, "context": "trendyol.com", "verdict": "unrelated", "reason": "that was shopping"},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil || result.IsError {
			t.Fatalf("%s = %+v, %v", name, result, err)
		}
	}
	bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "focus_session", Arguments: map[string]any{"action": "start"}})
	if !bad.IsError {
		t.Fatal("start without a task must be refused")
	}
	unsure, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "remember_for_task", Arguments: map[string]any{"task_id": 4, "context": "x", "verdict": "unsure", "reason": "x"}})
	if !unsure.IsError {
		t.Fatal("the owner's answer cannot be unsure")
	}
}

func TestSetDayPlanUsesTheOwnersLocalTime(t *testing.T) {
	repository := &planningRepository{}
	// 22:30 UTC on Sep 30 is already Oct 1 in Istanbul.
	session := connectPlanning(t, repository, time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC))
	task := int64(4)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_day_plan", Arguments: map[string]any{
		"blocks": []map[string]any{{"start": "09:30", "minutes": 90, "task_id": task}, {"start": "13:00", "minutes": 30, "title": "Shower"}},
		"reason": "owner approved the draft",
	}})
	if err != nil || result.IsError {
		t.Fatalf("set_day_plan = %+v, %v", result, err)
	}
	if repository.date.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("plan date = %s, want the local day 2026-10-01", repository.date)
	}
	if got := repository.plan.Blocks[0].StartTime; !got.Equal(time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC)) || *repository.plan.Blocks[0].ActivityID != 4 {
		t.Fatalf("first block = %+v", repository.plan.Blocks[0])
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output SetDayPlanOutput
	json.Unmarshal(encoded, &output)
	if len(output.Plan.Blocks) != 2 || output.Plan.Blocks[0].Start != "09:30" || output.Plan.Blocks[0].End != "11:00" {
		t.Fatalf("plan output = %+v", output.Plan)
	}
	bad, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_day_plan", Arguments: map[string]any{
		"blocks": []map[string]any{{"start": "9.30", "minutes": 30}}, "reason": "x"}})
	if !bad.IsError {
		t.Fatal("a malformed time must be a tool error")
	}
}

func connectPlanning(t *testing.T, repository store.Repository, now time.Time) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry-test", Version: "0"}, nil)
	addPlanningTools(server, repository, func() time.Time { return now })
	addFollowUpTools(server, repository, func() time.Time { return now })
	addRuleTools(server, repository)
	addScheduleTools(server, repository, func() time.Time { return now })
	addFocusTools(server, repository)
	addPatternTools(server, repository, func() time.Time { return now })
	addOutreachTools(server, repository, func() time.Time { return now })
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestMCPAcceptsItsPublicHostButNotOthersOnLoopback(t *testing.T) {
	handler, err := New(&toolsRepository{}, "token", "vps1.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	call := func(host string) int {
		request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		request.Host = host
		request.Header.Set("Authorization", "Bearer token")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	if got := call("vps1.example.ts.net:8443"); got != http.StatusOK {
		t.Fatalf("public host = %d", got)
	}
	if got := call("127.0.0.1"); got != http.StatusOK {
		t.Fatalf("loopback = %d", got)
	}
	if got := call("evil.example"); got != http.StatusForbidden {
		t.Fatalf("other host = %d, want 403", got)
	}
}
