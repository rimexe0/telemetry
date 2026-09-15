package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

func ownerTestServer(t *testing.T, repository *fakeRepository) *Server {
	t.Helper()
	server, err := New(repository, Config{
		IngestToken:     "ingest-secret",
		ReadToken:       "read-secret",
		OwnerAuthorized: func(*http.Request) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestPlanningEndpointsRejectMissingOwner(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/activities", nil),
		httptest.NewRequest(http.MethodPost, "/v1/activities", strings.NewReader(`{"title":"x"}`)),
		httptest.NewRequest(http.MethodGet, "/v1/plans/2026-09-15", nil),
		httptest.NewRequest(http.MethodPut, "/v1/plans/2026-09-15", strings.NewReader(`{"blocks":[]}`)),
		httptest.NewRequest(http.MethodPatch, "/v1/plans/2026-09-15/blocks/1", strings.NewReader(`{"state":"done"}`)),
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestPlanningEndpointsRejectReadToken(t *testing.T) {
	repository := &fakeRepository{}
	server := testServer(t, repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/activities", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read-token status = %d, want 401", response.Code)
	}
}

func TestCreateAndPatchActivity(t *testing.T) {
	repository := &fakeRepository{}
	server := ownerTestServer(t, repository)

	create := httptest.NewRequest(http.MethodPost, "/v1/activities", strings.NewReader(`{"title":"Practice piano","notes":"20 focused minutes","next_action":"Open the book at page 12","priority":5,"effort_estimate_minutes":20}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	var created store.Activity
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "Practice piano" || repository.activityInput.NextAction != "Open the book at page 12" {
		t.Fatalf("create did not carry fields: %+v", repository.activityInput)
	}

	patch := httptest.NewRequest(http.MethodPatch, "/v1/activities/7", strings.NewReader(`{"title":"Practice piano","status":"waiting","priority":5}`))
	patch.SetPathValue("id", "7")
	patchResponse := httptest.NewRecorder()
	server.ServeHTTP(patchResponse, patch)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	if repository.activityInput.ID != 7 || repository.activityInput.Status != "waiting" {
		t.Fatalf("patch did not carry id/status: %+v", repository.activityInput)
	}

	repository.activityErr = errors.New("title is required")
	invalid := httptest.NewRequest(http.MethodPost, "/v1/activities", strings.NewReader(`{"title":"  ","status":"someday"}`))
	invalidResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid activity status = %d, want 400", invalidResponse.Code)
	}
}

func TestGetPlanReturnsNullWhenUnplanned(t *testing.T) {
	repository := &fakeRepository{}
	server := ownerTestServer(t, repository)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/plans/2026-09-15", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("get plan status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"plan":null`) {
		t.Fatalf("expected null plan, got %s", response.Body.String())
	}
	if !repository.planDate.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected plan date %v", repository.planDate)
	}
}

func TestSaveAndReadBackPlan(t *testing.T) {
	repository := &fakeRepository{}
	server := ownerTestServer(t, repository)
	body := `{"note":"morning plan","blocks":[{"activity_id":3,"start_time":"2026-09-15T09:00:00+03:00","duration_minutes":50},{"title":"Rest","start_time":"2026-09-15T10:00:00+03:00","duration_minutes":15,"fixed":true}]}`
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/v1/plans/2026-09-15", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("save plan status = %d: %s", response.Code, response.Body.String())
	}
	var saved store.Plan
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.PlanDate != "2026-09-15" || len(saved.Blocks) != 2 {
		t.Fatalf("unexpected saved plan: %+v", saved)
	}
	if _, offset := repository.planInput.Blocks[0].StartTime.Zone(); offset != 3*3600 {
		t.Fatalf("block start time lost its zone: %v", repository.planInput.Blocks[0].StartTime)
	}
	if repository.planInput.Blocks[1].Title != "Rest" {
		t.Fatalf("literal block title not kept: %+v", repository.planInput.Blocks[1])
	}

	repository.planErr = errors.New("block 1 duration_minutes must be between 1 and 1440")
	invalid := httptest.NewRequest(http.MethodPut, "/v1/plans/2026-09-15", strings.NewReader(`{"blocks":[{"title":"Rest","duration_minutes":0}]}`))
	invalidResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid plan status = %d, want 400", invalidResponse.Code)
	}
}

func TestPlanBlockStateUpdate(t *testing.T) {
	repository := &fakeRepository{}
	repository.blockState = store.PlanBlock{ID: 4, State: "switched", StateNote: "agent finished it instead"}
	server := ownerTestServer(t, repository)
	request := httptest.NewRequest(http.MethodPatch, "/v1/plans/2026-09-15/blocks/4", strings.NewReader(`{"state":"switched","state_note":"agent finished it instead"}`))
	request.SetPathValue("id", "4")
	request.SetPathValue("date", "2026-09-15")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("block state status = %d: %s", response.Code, response.Body.String())
	}
	if repository.blockID != 4 || repository.blockStateName != "switched" {
		t.Fatalf("state update not carried: id=%d state=%s", repository.blockID, repository.blockStateName)
	}

	repository.blockErr = store.ErrNotFound
	missing := httptest.NewRequest(http.MethodPatch, "/v1/plans/2026-09-15/blocks/99", strings.NewReader(`{"state":"done"}`))
	missing.SetPathValue("id", "99")
	missing.SetPathValue("date", "2026-09-15")
	missingResponse := httptest.NewRecorder()
	server.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing block status = %d, want 404", missingResponse.Code)
	}
}

var _ = context.Background
