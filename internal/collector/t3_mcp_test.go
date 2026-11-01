package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeT3MCP answers like T3's MCP server: one project, one thread with a
// run the owner started and a run an agent started, messages over two pages.
func fakeT3MCP(t *testing.T, updatedAt *string, reads *int) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		if request.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "s1")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *request.ID, "result": map[string]any{"protocolVersion": t3MCPProtocol}})
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "s1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var structured any
		switch request.Params.Name {
		case "t3_project_list":
			structured = map[string]any{"projects": []any{map[string]any{"id": "p1", "title": "Telemetry", "workspaceRoot": "/code/telemetry",
				"createdAt": "2026-10-01T10:00:00Z", "updatedAt": "2026-10-01T10:00:00Z", "deletedAt": nil}}, "nextCursor": nil}
		case "t3_thread_list":
			structured = map[string]any{"threads": []any{map[string]any{"threadId": "t1", "updatedAt": *updatedAt}}, "nextCursor": nil}
		case "t3_thread_read":
			*reads++
			if request.Params.Arguments["maxCharsPerItem"] != float64(1) {
				t.Errorf("message text was asked for: %v", request.Params.Arguments)
			}
			thread := map[string]any{"threadId": "t1", "projectId": "p1", "title": "Fix sleep", "createdBy": "user", "creationSource": "web",
				"status": "completed", "activeRunId": nil, "providerInstanceId": "claudeAgent", "model": "claude-opus-5-5", "runtimeMode": "auto",
				"interactionMode": "default", "branch": nil, "worktreePath": nil, "parentThreadId": nil, "pendingRequestCount": 1,
				"archived": false, "settled": false, "createdAt": "2026-11-01T09:00:00Z", "updatedAt": *updatedAt}
			runs := []any{
				map[string]any{"runId": "r1", "status": "completed", "requestedAt": "2026-11-01T09:00:00Z", "startedAt": "2026-11-01T09:00:01Z", "completedAt": "2026-11-01T09:05:00Z"},
				map[string]any{"runId": "r2", "status": "running", "requestedAt": "2026-11-01T09:10:00Z", "startedAt": "2026-11-01T09:10:01Z", "completedAt": nil},
			}
			if request.Params.Arguments["afterPosition"] == nil {
				structured = map[string]any{"thread": thread, "recentRuns": runs, "nextPosition": 4, "hasMore": true, "items": []any{
					map[string]any{"position": 0, "runId": "r1", "type": "user_message", "createdBy": "user", "text": "s", "updatedAt": "2026-11-01T09:00:00Z"},
					map[string]any{"position": 4, "runId": "r1", "type": "assistant_message", "createdBy": "agent", "text": "D", "updatedAt": "2026-11-01T09:04:00Z"},
				}}
			} else {
				structured = map[string]any{"thread": thread, "recentRuns": runs, "nextPosition": 9, "hasMore": false, "items": []any{
					map[string]any{"position": 9, "runId": "r2", "type": "user_message", "createdBy": "agent", "text": "R", "updatedAt": "2026-11-01T09:10:00Z"},
				}}
			}
		default:
			t.Errorf("unexpected tool %s", request.Params.Name)
		}
		// T3 answers tool calls as a short event stream.
		w.Header().Set("Content-Type", "text/event-stream")
		answer, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *request.ID, "result": map[string]any{"structuredContent": structured, "isError": false}})
		w.Write([]byte("event: message\ndata: " + string(answer) + "\n\n"))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestT3MCPRecordsWhoStartedEachTurn(t *testing.T) {
	updatedAt, reads := "2026-11-01T09:10:00Z", 0
	server := fakeT3MCP(t, &updatedAt, &reads)
	reader := newT3MCP(server.URL, "secret")
	rows, err := reader.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]t3Row{}
	for _, row := range rows {
		byKey[row.key] = row
		encoded, _ := json.Marshal(row.payload)
		if strings.Contains(string(encoded), `"text"`) {
			t.Fatalf("%s keeps message text: %s", row.key, encoded)
		}
	}
	if byKey["project:p1"].payload["workspace_root"] != "/code/telemetry" {
		t.Fatalf("project = %v", byKey["project:p1"].payload)
	}
	thread := byKey["thread:t1"].payload
	// The agent's later message does not count as the owner's.
	if thread["latest_user_message_at"] != "2026-11-01T09:00:00Z" || thread["pending_user_input_count"] != 1 || thread["created_by"] != "user" {
		t.Fatalf("thread = %v", thread)
	}
	if got := byKey["turn:t1:r1"].payload["requested_by"]; got != "user" {
		t.Fatalf("r1 requested by %v", got)
	}
	if turn := byKey["turn:t1:r2"]; turn.payload["requested_by"] != "agent" || turn.payload["state"] != "running" || turn.ended != nil {
		t.Fatalf("r2 = %v", turn)
	}
	if reads != 2 {
		t.Fatalf("first read took %d pages, want 2", reads)
	}
	// An unchanged thread is not read again.
	if _, err := reader.read(context.Background()); err != nil || reads != 2 {
		t.Fatalf("unchanged thread read again: reads=%d err=%v", reads, err)
	}
	updatedAt = "2026-11-01T09:20:00Z"
	if _, err := reader.read(context.Background()); err != nil || reads != 3 {
		t.Fatalf("updated thread: reads=%d err=%v", reads, err)
	}
}

func TestT3MCPRefusedCredential(t *testing.T) {
	updatedAt, reads := "x", 0
	server := fakeT3MCP(t, &updatedAt, &reads)
	_, err := newT3MCP(server.URL, "wrong").read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refused the MCP credential") {
		t.Fatalf("err = %v", err)
	}
}
