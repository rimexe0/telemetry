package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

func TestAlignmentAndSummaryEndpointsRequireOwnerAuthorization(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/alignment/current", nil),
		httptest.NewRequest(http.MethodGet, "/v1/alignment/history?from=2026-09-23T00:00:00Z&to=2026-09-24T00:00:00Z", nil),
		httptest.NewRequest(http.MethodGet, "/v1/execution/summary?from=2026-09-23T00:00:00Z&to=2026-09-24T00:00:00Z", nil),
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestCurrentAlignmentUsesLinkedExecutionSession(t *testing.T) {
	activityID := int64(1)
	blockID := int64(10)
	blockStart := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	session := &store.ExecutionSession{ID: 7, ActivityID: activityID, PlanBlockID: &blockID, StartedAt: blockStart, State: "active", Events: []store.ExecutionEvent{{EventType: "started", OccurredAt: blockStart}}}
	repository := &fakeRepository{
		activities:       []store.Activity{{ID: activityID, Title: "Write the outline", NextAction: "Open the outline file"}},
		plan:             &store.Plan{PlanDate: "2026-09-23", VersionID: 4, Blocks: []store.PlanBlock{{ID: blockID, ActivityID: &activityID, Title: "Write the outline", StartTime: blockStart, DurationMinutes: 60, State: "planned"}}},
		executionCurrent: session,
	}
	server := ownerTestServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/alignment/current?at=2026-09-23T10:15:00Z", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("alignment status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"alignment_state":"aligned"`) || !strings.Contains(body, `"plan_block_id":10`) || !strings.Contains(body, `"execution_session_id":7`) {
		t.Fatalf("alignment response = %s", body)
	}
}

func TestCurrentAlignmentReportsApprovedWaitingInterleaveSeparately(t *testing.T) {
	activityID := int64(1)
	otherActivityID := int64(2)
	blockID := int64(10)
	primaryID := int64(7)
	blockStart := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	primary := &store.ExecutionSession{
		ID:          primaryID,
		ActivityID:  activityID,
		PlanBlockID: &blockID,
		Role:        store.ExecutionRolePrimary,
		StartedAt:   blockStart,
		State:       "waiting_external",
		Events:      []store.ExecutionEvent{{EventType: "started", OccurredAt: blockStart}, {EventType: "waiting_external", OccurredAt: blockStart.Add(5 * time.Minute)}},
	}
	interleave := store.ExecutionSession{
		ID:               8,
		ActivityID:       otherActivityID,
		Role:             store.ExecutionRoleWaitingInterleave,
		PrimarySessionID: &primaryID,
		StartedAt:        blockStart.Add(10 * time.Minute),
		State:            "active",
	}
	repository := &fakeRepository{
		activities:           []store.Activity{{ID: activityID, Title: "Write the outline"}, {ID: otherActivityID, Title: "Payroll export"}},
		plan:                 &store.Plan{PlanDate: "2026-09-23", VersionID: 4, Blocks: []store.PlanBlock{{ID: blockID, ActivityID: &activityID, Title: "Write the outline", StartTime: blockStart, DurationMinutes: 60, State: "planned"}}},
		executionPrimary:     primary,
		executionInterleaves: []store.ExecutionSession{interleave},
		sources:              []store.SourceSummary{{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", LastCollectedAt: timePtrHTTP(time.Now().UTC())}},
		observations: []store.ObservationView{{
			SourceInstanceKey: "activitywatch:mac:window",
			SourceKind:        "activitywatch",
			SourceRecordKey:   "payroll-window",
			OccurredAt:        blockStart.Add(12 * time.Minute),
			CollectedAt:       time.Now().UTC(),
			OriginalPayload:   []byte(`{"data":{"app":"Excel","title":"Payroll export"}}`),
		}},
	}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/alignment/current?at=2026-09-23T10:15:00Z", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"alignment_state":"aligned"`) || !strings.Contains(body, `"reason_code":"useful_interleave"`) || !strings.Contains(body, `"execution_session_id":7`) || !strings.Contains(body, `"attended_execution_session_id":8`) {
		t.Fatalf("interleave alignment = %d: %s", response.Code, body)
	}
	primary.Events = append(primary.Events, store.ExecutionEvent{EventType: "external_ready", OccurredAt: blockStart.Add(14 * time.Minute)})
	pendingResponse := httptest.NewRecorder()
	server.ServeHTTP(pendingResponse, httptest.NewRequest(http.MethodGet, "/v1/alignment/current?at=2026-09-23T10:15:00Z", nil))
	if pendingResponse.Code != http.StatusOK || !strings.Contains(pendingResponse.Body.String(), `"reason_code":"return_pending"`) || !strings.Contains(pendingResponse.Body.String(), `"execution_session_id":7`) || !strings.Contains(pendingResponse.Body.String(), `"attended_execution_session_id":8`) {
		t.Fatalf("ready interleave alignment = %d: %s", pendingResponse.Code, pendingResponse.Body.String())
	}
}

func TestCurrentAlignmentReportsStaleEvidenceAsUnknown(t *testing.T) {
	blockID := int64(10)
	activityID := int64(1)
	blockStart := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	repository := &fakeRepository{
		activities:   []store.Activity{{ID: activityID, Title: "Write the outline"}},
		plan:         &store.Plan{PlanDate: "2026-09-23", VersionID: 4, Blocks: []store.PlanBlock{{ID: blockID, ActivityID: &activityID, Title: "Write the outline", StartTime: blockStart, DurationMinutes: 60, State: "planned"}}},
		sources:      []store.SourceSummary{{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", LastCollectedAt: timePtrHTTP(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))}},
		observations: []store.ObservationView{{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", SourceRecordKey: "one", OccurredAt: blockStart.Add(10 * time.Minute), CollectedAt: blockStart.Add(11 * time.Minute), OriginalPayload: []byte(`{"data":{"app":"YouTube","title":"Music"}}`)}},
	}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/alignment/current?at=2026-09-23T10:15:00Z", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"alignment_state":"unknown"`) || !strings.Contains(response.Body.String(), `"reason_code":"stale_evidence"`) {
		t.Fatalf("stale alignment = %d: %s", response.Code, response.Body.String())
	}
}

func TestAlignmentHistoryReturnsPlanAndExecutionReferences(t *testing.T) {
	activityID := int64(1)
	blockID := int64(10)
	blockStart := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	session := &store.ExecutionSession{ID: 7, ActivityID: activityID, PlanBlockID: &blockID, StartedAt: blockStart, State: "active", Events: []store.ExecutionEvent{{EventType: "started", OccurredAt: blockStart}}}
	repository := &fakeRepository{
		activities:        []store.Activity{{ID: activityID, Title: "Write the outline"}},
		plan:              &store.Plan{PlanDate: "2026-09-23", VersionID: 4, Blocks: []store.PlanBlock{{ID: blockID, ActivityID: &activityID, Title: "Write the outline", StartTime: blockStart, DurationMinutes: 60, State: "planned"}}},
		executionSessions: []store.ExecutionSession{{ID: session.ID, ActivityID: activityID, PlanBlockID: &blockID, StartedAt: blockStart, State: "active"}},
		executionSession:  session,
	}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/alignment/history?from=2026-09-23T00:00:00Z&to=2026-09-24T00:00:00Z&limit=10", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"plan_block_id":10`) || !strings.Contains(response.Body.String(), `"execution_session_id":7`) {
		t.Fatalf("history = %d: %s", response.Code, response.Body.String())
	}
}

func TestAlignmentHistoryAndSummaryRejectInvalidWindows(t *testing.T) {
	server := ownerTestServer(t, &fakeRepository{})
	for _, path := range []string{
		"/v1/alignment/history?from=2026-09-24T00:00:00Z&to=2026-09-23T00:00:00Z",
		"/v1/execution/summary?from=2026-09-23T00:00:00Z&to=2026-11-01T00:00:00Z",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestExecutionSummaryDerivesEventMetrics(t *testing.T) {
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	blockID := int64(10)
	plannedStart := start.Add(-10 * time.Minute)
	session := &store.ExecutionSession{ID: 8, PlanBlockID: &blockID, StartedAt: start, EndedAt: &end, State: "completed", Events: []store.ExecutionEvent{{EventType: "started", OccurredAt: start}, {EventType: "interrupted", OccurredAt: start.Add(10 * time.Minute)}, {EventType: "resumed", OccurredAt: start.Add(20 * time.Minute)}}}
	repository := &fakeRepository{
		executionSessions: []store.ExecutionSession{{ID: session.ID, PlanBlockID: &blockID, StartedAt: session.StartedAt, EndedAt: session.EndedAt, State: session.State}},
		executionSession:  session,
		planBlocks:        []store.PlanBlock{{ID: blockID, StartTime: plannedStart, DurationMinutes: 50}},
	}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/execution/summary?from=2026-09-23T00:00:00Z&to=2026-09-24T00:00:00Z", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"return_count":1`) || !strings.Contains(response.Body.String(), `"initiation_latencies_seconds":[600]`) || !strings.Contains(response.Body.String(), `"planned_seconds":3000`) || !strings.Contains(response.Body.String(), `"error_seconds":600`) || strings.Contains(response.Body.String(), "productivity_score") {
		t.Fatalf("summary = %d: %s", response.Code, response.Body.String())
	}
}

func TestExecutionSummarySeparatesAndTracesSwitchKinds(t *testing.T) {
	planDate := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	firstStart := planDate.Add(10 * time.Hour)
	secondStart := planDate.Add(11 * time.Hour)
	activityID := int64(1)
	otherActivityID := int64(2)
	firstBlockID := int64(10)
	secondBlockID := int64(11)
	firstEnd := firstStart.Add(30 * time.Minute)
	firstBlock := store.PlanBlock{ID: firstBlockID, ActivityID: &activityID, Title: "Write the outline", StartTime: firstStart, DurationMinutes: 30, State: "planned"}
	secondBlock := store.PlanBlock{ID: secondBlockID, ActivityID: &activityID, Title: "Continue the outline", StartTime: secondStart, DurationMinutes: 30, State: "planned"}
	session := &store.ExecutionSession{
		ID:          8,
		ActivityID:  activityID,
		PlanBlockID: &firstBlockID,
		StartedAt:   firstStart,
		EndedAt:     &firstEnd,
		State:       "interrupted",
		Events: []store.ExecutionEvent{
			{ID: 101, SessionID: 8, EventType: "started", OccurredAt: firstStart},
			{ID: 102, SessionID: 8, EventType: "switched_out", OccurredAt: firstStart.Add(20 * time.Minute)},
		},
	}
	lastCollected := time.Now().UTC().Add(-time.Minute)
	repository := &fakeRepository{
		activities: []store.Activity{
			{ID: activityID, Title: "Write the outline", Status: "active"},
			{ID: otherActivityID, Title: "Payroll export", Status: "active"},
		},
		plan:       &store.Plan{PlanDate: "2026-09-23", VersionID: 4, Blocks: []store.PlanBlock{firstBlock, secondBlock}},
		planBlocks: []store.PlanBlock{firstBlock, secondBlock},
		sources:    []store.SourceSummary{{SourceInstanceKey: "activitywatch:mac:window", SourceKind: "activitywatch", LastCollectedAt: &lastCollected}},
		observations: []store.ObservationView{{
			SourceInstanceKey: "activitywatch:mac:window",
			SourceKind:        "activitywatch",
			SourceRecordKey:   "event-payroll",
			OccurredAt:        secondStart.Add(5 * time.Minute),
			CollectedAt:       time.Now().UTC(),
			OriginalPayload:   []byte(`{"data":{"app":"Excel","title":"Payroll export"}}`),
		}},
		executionSessions: []store.ExecutionSession{{ID: session.ID, ActivityID: activityID, PlanBlockID: &firstBlockID, StartedAt: session.StartedAt, EndedAt: session.EndedAt, State: session.State}},
		executionSession:  session,
	}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/execution/summary?from=2026-09-23T00:00:00Z&to=2026-09-23T21:00:00Z", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"intentional_switch_count":1`) || !strings.Contains(body, `"possible_unplanned_switch_count":1`) {
		t.Fatalf("switch summary = %d: %s", response.Code, body)
	}
	if !strings.Contains(body, `"metric":"intentional_switch_count","session_id":8,"plan_block_id":10,"event_ids":[102],"value":1`) || !strings.Contains(body, `"plan_block_id":11`) || !strings.Contains(body, `"source_record_key":"event-payroll"`) || !strings.Contains(body, `"related_activity_id":2`) {
		t.Fatalf("switch evidence is not traceable: %s", body)
	}
}

func TestExecutionSummaryMarksSaturatedSessionWindowBounded(t *testing.T) {
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	session := &store.ExecutionSession{
		ID:        8,
		StartedAt: start,
		EndedAt:   &end,
		State:     "completed",
		Events: []store.ExecutionEvent{
			{EventType: "started", OccurredAt: start},
			{EventType: "interrupted", OccurredAt: start.Add(10 * time.Minute)},
			{EventType: "resumed", OccurredAt: start.Add(20 * time.Minute)},
		},
	}
	candidates := make([]store.ExecutionSession, maxAlignmentResults)
	for index := range candidates {
		candidates[index] = store.ExecutionSession{ID: session.ID, StartedAt: start, EndedAt: &end, State: session.State}
	}
	server := ownerTestServer(t, &fakeRepository{executionSessions: candidates, executionSession: session})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/execution/summary?from=2026-09-23T09:00:00Z&to=2026-09-23T12:00:00Z", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"coverage":"bounded"`) || !strings.Contains(response.Body.String(), `"return_rate":null`) || !strings.Contains(response.Body.String(), `"abandonment_rate":null`) || !strings.Contains(response.Body.String(), `"recovery_same_task_return_rate":null`) {
		t.Fatalf("saturated summary = %d: %s", response.Code, response.Body.String())
	}
}

func timePtrHTTP(value time.Time) *time.Time { return &value }
