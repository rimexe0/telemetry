package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
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
