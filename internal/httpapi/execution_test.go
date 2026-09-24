package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rimexe0/telemetry/internal/store"
)

func TestExecutionEndpointsRequireOwnerAuthorization(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/execution/current", nil),
		httptest.NewRequest(http.MethodGet, "/v1/execution/primary", nil),
		httptest.NewRequest(http.MethodGet, "/v1/execution/attended", nil),
		httptest.NewRequest(http.MethodGet, "/v1/execution/sessions", nil),
		httptest.NewRequest(http.MethodPost, "/v1/execution/sessions", strings.NewReader(`{"activity_id":1}`)),
		httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/1", nil),
		httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/1/interleaves", nil),
		httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/1/events", strings.NewReader(`{"event_type":"completed"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/1/return-to-primary", nil),
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.Method, request.URL.Path, response.Code)
		}
	}

	readRequest := httptest.NewRequest(http.MethodGet, "/v1/execution/current", nil)
	readRequest.Header.Set("Authorization", "Bearer read-secret")
	readResponse := httptest.NewRecorder()
	server.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusUnauthorized {
		t.Fatalf("read-token status = %d, want 401", readResponse.Code)
	}
}

func TestExecutionStartEventAndCurrentFlow(t *testing.T) {
	activityID := int64(3)
	planBlockID := int64(9)
	session := &store.ExecutionSession{
		ID:                 7,
		ActivityID:         activityID,
		PlanBlockID:        &planBlockID,
		State:              "active",
		NextActionSnapshot: "Open the task",
		Events:             []store.ExecutionEvent{{ID: 1, SessionID: 7, EventType: "started"}},
	}
	repository := &fakeRepository{executionSession: session, executionCurrent: session}
	server := ownerTestServer(t, repository)

	start := httptest.NewRequest(http.MethodPost, "/v1/execution/sessions", strings.NewReader(`{"activity_id":3,"plan_block_id":9,"started_at":"2026-09-22T10:00:00Z"}`))
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, start)
	if startResponse.Code != http.StatusCreated {
		t.Fatalf("start status = %d: %s", startResponse.Code, startResponse.Body.String())
	}
	if repository.executionStartInput.ActivityID != activityID || repository.executionStartInput.PlanBlockID == nil || *repository.executionStartInput.PlanBlockID != planBlockID {
		t.Fatalf("start input = %+v", repository.executionStartInput)
	}
	var started store.ExecutionSession
	if err := json.Unmarshal(startResponse.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.ID != 7 || started.State != "active" || len(started.Events) != 1 || started.Events[0].EventType != "started" {
		t.Fatalf("start response = %+v", started)
	}

	marker := "Resume at the first paragraph"
	event := httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/7/events", strings.NewReader(`{"event_type":"switched_out","occurred_at":"2026-09-22T10:20:00Z","note":"Switching deliberately","resume_marker":"Resume at the first paragraph","metadata":{"reason_category":"intentional_switch"}}`))
	eventResponse := httptest.NewRecorder()
	server.ServeHTTP(eventResponse, event)
	if eventResponse.Code != http.StatusOK {
		t.Fatalf("event status = %d: %s", eventResponse.Code, eventResponse.Body.String())
	}
	if repository.executionEventSession != 7 || repository.executionEventInput.EventType != "switched_out" || repository.executionEventInput.ResumeMarker == nil || *repository.executionEventInput.ResumeMarker != marker || !strings.Contains(string(repository.executionEventInput.Metadata), `"reason_category":"intentional_switch"`) {
		t.Fatalf("event input = %+v", repository.executionEventInput)
	}

	currentResponse := httptest.NewRecorder()
	server.ServeHTTP(currentResponse, httptest.NewRequest(http.MethodGet, "/v1/execution/current", nil))
	if currentResponse.Code != http.StatusOK || !strings.Contains(currentResponse.Body.String(), `"session"`) || !strings.Contains(currentResponse.Body.String(), `"next_action_snapshot":"Open the task"`) {
		t.Fatalf("current response = %d: %s", currentResponse.Code, currentResponse.Body.String())
	}

	readResponse := httptest.NewRecorder()
	server.ServeHTTP(readResponse, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/7", nil))
	if readResponse.Code != http.StatusOK || !strings.Contains(readResponse.Body.String(), `"event_type":"started"`) {
		t.Fatalf("read response = %d: %s", readResponse.Code, readResponse.Body.String())
	}
}

func TestExecutionPrimaryAttendedAndInterleaveReturnRoutes(t *testing.T) {
	primaryID := int64(7)
	primary := &store.ExecutionSession{ID: primaryID, ActivityID: 3, Role: store.ExecutionRolePrimary, State: "waiting_external"}
	interleave := store.ExecutionSession{ID: 8, ActivityID: 4, Role: store.ExecutionRoleWaitingInterleave, PrimarySessionID: &primaryID, State: "active"}
	repository := &fakeRepository{
		executionPrimary:     primary,
		executionAttended:    &interleave,
		executionInterleaves: []store.ExecutionSession{interleave},
		executionReturn: store.ExecutionReturn{
			PrimarySession:    store.ExecutionSession{ID: primaryID, Role: store.ExecutionRolePrimary, State: "active"},
			InterleaveSession: store.ExecutionSession{ID: interleave.ID, Role: store.ExecutionRoleWaitingInterleave, PrimarySessionID: &primaryID, State: "interrupted"},
		},
	}
	server := ownerTestServer(t, repository)

	primaryResponse := httptest.NewRecorder()
	server.ServeHTTP(primaryResponse, httptest.NewRequest(http.MethodGet, "/v1/execution/primary", nil))
	if primaryResponse.Code != http.StatusOK || !strings.Contains(primaryResponse.Body.String(), `"role":"primary"`) || !strings.Contains(primaryResponse.Body.String(), `"state":"waiting_external"`) {
		t.Fatalf("primary response = %d: %s", primaryResponse.Code, primaryResponse.Body.String())
	}

	attendedResponse := httptest.NewRecorder()
	server.ServeHTTP(attendedResponse, httptest.NewRequest(http.MethodGet, "/v1/execution/attended", nil))
	if attendedResponse.Code != http.StatusOK || !strings.Contains(attendedResponse.Body.String(), `"role":"waiting_interleave"`) || !strings.Contains(attendedResponse.Body.String(), `"primary_session_id":7`) {
		t.Fatalf("attended response = %d: %s", attendedResponse.Code, attendedResponse.Body.String())
	}

	interleavesResponse := httptest.NewRecorder()
	server.ServeHTTP(interleavesResponse, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/7/interleaves?limit=3", nil))
	if interleavesResponse.Code != http.StatusOK || repository.executionInterleaveLimit != 3 || !strings.Contains(interleavesResponse.Body.String(), `"id":8`) {
		t.Fatalf("interleaves response = %d, limit=%d: %s", interleavesResponse.Code, repository.executionInterleaveLimit, interleavesResponse.Body.String())
	}
	tooManyInterleaves := httptest.NewRecorder()
	server.ServeHTTP(tooManyInterleaves, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/7/interleaves?limit=101", nil))
	if tooManyInterleaves.Code != http.StatusBadRequest {
		t.Fatalf("unbounded interleave limit status = %d, want 400", tooManyInterleaves.Code)
	}

	returnRequest := httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/8/return-to-primary", strings.NewReader(`{"primary_resume_marker":"Continue the outline","interleave_resume_marker":"Resume at page four"}`))
	returnResponse := httptest.NewRecorder()
	server.ServeHTTP(returnResponse, returnRequest)
	if returnResponse.Code != http.StatusOK || !strings.Contains(returnResponse.Body.String(), `"primary_session"`) || !strings.Contains(returnResponse.Body.String(), `"interleave_session"`) {
		t.Fatalf("return response = %d: %s", returnResponse.Code, returnResponse.Body.String())
	}
	if repository.executionReturnInput.PrimaryResumeMarker == nil || *repository.executionReturnInput.PrimaryResumeMarker != "Continue the outline" || repository.executionReturnInput.InterleaveResumeMarker == nil || *repository.executionReturnInput.InterleaveResumeMarker != "Resume at page four" {
		t.Fatalf("return input = %+v", repository.executionReturnInput)
	}

	startRequest := httptest.NewRequest(http.MethodPost, "/v1/execution/sessions", strings.NewReader(`{"activity_id":4,"role":"waiting_interleave","primary_session_id":7}`))
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusCreated || repository.executionStartInput.Role != store.ExecutionRoleWaitingInterleave || repository.executionStartInput.PrimarySessionID == nil || *repository.executionStartInput.PrimarySessionID != primaryID {
		t.Fatalf("interleave start = %d input %+v: %s", startResponse.Code, repository.executionStartInput, startResponse.Body.String())
	}
}

func TestExecutionConflictUsesHTTP409(t *testing.T) {
	repository := &fakeRepository{executionErr: store.ErrExecutionConflict}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/execution/sessions", strings.NewReader(`{"activity_id":4}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"execution_conflict"`) {
		t.Fatalf("conflict response = %d: %s", response.Code, response.Body.String())
	}
}

func TestExecutionRoutesBoundIDsTransitionsAndJSON(t *testing.T) {
	repository := &fakeRepository{executionSession: &store.ExecutionSession{ID: 4, State: "active"}}
	server := ownerTestServer(t, repository)

	badID := httptest.NewRecorder()
	server.ServeHTTP(badID, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/nope", nil))
	if badID.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d, want 400", badID.Code)
	}
	badEventID := httptest.NewRecorder()
	server.ServeHTTP(badEventID, httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/nope/events", strings.NewReader(`{"event_type":"completed"}`)))
	if badEventID.Code != http.StatusBadRequest {
		t.Fatalf("bad event id status = %d, want 400", badEventID.Code)
	}

	repository.executionErr = store.ErrInvalidExecutionTransition
	badEvent := httptest.NewRecorder()
	server.ServeHTTP(badEvent, httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/4/events", strings.NewReader(`{"event_type":"started"}`)))
	if badEvent.Code != http.StatusBadRequest {
		t.Fatalf("bad event status = %d, want 400", badEvent.Code)
	}

	repository.executionErr = store.ErrNotFound
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions/99", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing session status = %d, want 404", missing.Code)
	}

	repository.executionErr = nil
	extraJSON := httptest.NewRecorder()
	server.ServeHTTP(extraJSON, httptest.NewRequest(http.MethodPost, "/v1/execution/sessions/4/events", strings.NewReader(`{"event_type":"completed"}{}`)))
	if extraJSON.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d, want 400", extraJSON.Code)
	}
}

func TestExecutionListUsesBoundedLimit(t *testing.T) {
	repository := &fakeRepository{executionSessions: []store.ExecutionSession{{ID: 5}}}
	server := ownerTestServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/execution/sessions?limit=3", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || repository.executionLimit != 3 || !strings.Contains(response.Body.String(), `"id":5`) {
		t.Fatalf("list response = %d, limit=%d: %s", response.Code, repository.executionLimit, response.Body.String())
	}

	tooLarge := httptest.NewRecorder()
	server.ServeHTTP(tooLarge, httptest.NewRequest(http.MethodGet, "/v1/execution/sessions?limit=201", nil))
	if tooLarge.Code != http.StatusBadRequest {
		t.Fatalf("large limit status = %d, want 400", tooLarge.Code)
	}
}
