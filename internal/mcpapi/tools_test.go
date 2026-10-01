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
	date time.Time
	plan store.PlanInput
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
