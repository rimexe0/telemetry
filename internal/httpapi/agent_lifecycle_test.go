package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

func agentRepository(threadPath string) *fakeRepository {
	waitAt := time.Now().UTC().Add(-10 * time.Minute)
	threadPayload, _ := json.Marshal(map[string]any{"thread_id": "t1", "worktree_path": threadPath})
	turnPayload, _ := json.Marshal(map[string]any{"thread_id": "t1", "turn_id": "u1", "state": "completed"})
	finished := waitAt.Add(5 * time.Minute)
	return &fakeRepository{
		executionPrimary: &store.ExecutionSession{ID: 7, ActivityID: 1, Role: store.ExecutionRolePrimary, State: "waiting_external", Events: []store.ExecutionEvent{{ID: 1, EventType: "waiting_external", OccurredAt: waitAt}}},
		contextRules:     []store.ActivityContextRule{{ID: 1, ActivityID: 1, Signal: store.ContextSignalPathPrefix, Pattern: "/work/client"}},
		sources:          []store.SourceSummary{{SourceInstanceKey: "t3code:mac:state", SourceKind: "t3code"}},
		observations: []store.ObservationView{
			{SourceInstanceKey: "t3code:mac:state", SourceKind: "t3code", Kind: "t3code.thread", SourceRecordKey: "thread:t1", OccurredAt: waitAt, OriginalPayload: threadPayload},
			{SourceInstanceKey: "t3code:mac:state", SourceKind: "t3code", Kind: "t3code.turn", SourceRecordKey: "turn:t1:id:u1", OccurredAt: finished, EndedAt: &finished, OriginalPayload: turnPayload},
		},
	}
}

func ingestTurn(t *testing.T, server *Server) {
	t.Helper()
	body := `{"id":"b-` + time.Now().Format("150405.000000000") + `","device_id":"mac","collector_version":"test","observations":[{"source_instance_key":"t3code:mac:state","source_kind":"t3code","device_id":"mac","source_record_key":"turn:t1:id:u1","kind":"t3code.turn","occurred_at":"2026-09-25T09:00:00Z","collected_at":"2026-09-25T09:00:01Z","original_payload":{"thread_id":"t1","state":"completed"}}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/ingest/batches", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer ingest-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d: %s", response.Code, response.Body)
	}
}

func TestIngestedAgentCompletionRecordsOneReturnOpportunity(t *testing.T) {
	repository := agentRepository("/work/client/app")
	server := testServer(t, repository)
	ingestTurn(t, server)
	ingestTurn(t, server) // repeated polling of the same state
	if len(repository.agentReady) != 1 {
		t.Fatalf("readiness recorded %d times", len(repository.agentReady))
	}
	if repository.executionPrimary.State != "waiting_external" {
		t.Fatalf("primary state changed to %s", repository.executionPrimary.State)
	}
	if repository.agentReady[0].Lifecycle.SourceRecordKey != "turn:t1:id:u1" {
		t.Fatalf("candidate = %+v", repository.agentReady[0])
	}
}

func TestUnknownAgentSessionDoesNotMutateExecution(t *testing.T) {
	repository := agentRepository("/tmp/unmapped")
	server := testServer(t, repository)
	ingestTurn(t, server)
	if len(repository.agentReady) != 0 || len(repository.executionPrimary.Events) != 1 {
		t.Fatalf("unknown agent session mutated execution: %+v", repository.executionPrimary.Events)
	}
}
