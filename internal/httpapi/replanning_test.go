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

func replanFixture() *fakeRepository {
	start := time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC)
	return &fakeRepository{plan: &store.Plan{PlanDate: "2026-09-25", VersionID: 10, Blocks: []store.PlanBlock{
		{ID: 1, Title: "Write", StartTime: start, DurationMinutes: 60, State: "active"},
		{ID: 2, Title: "Call", StartTime: start.Add(90 * time.Minute), DurationMinutes: 30, Fixed: true, State: "planned"},
	}}}
}

func serve(t *testing.T, server *Server, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
	var payload map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	return response, payload
}

const disruptionBody = `{"base_plan_version_id":10,"reason":"doctor ran late","disruption":{"start":"2026-09-25T06:15:00Z","end":"2026-09-25T07:00:00Z","available_until":"2026-09-25T15:00:00Z"}}`

func TestReplanProposalRoutesRequireOwner(t *testing.T) {
	server := testServer(t, replanFixture())
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", strings.NewReader(disruptionBody)),
		httptest.NewRequest(http.MethodGet, "/v1/plans/2026-09-25/replan-proposals", nil),
		httptest.NewRequest(http.MethodGet, "/v1/plans/2026-09-25/replan-proposals/1", nil),
		httptest.NewRequest(http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/accept", strings.NewReader(`{"content_hash":"x"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/reject", strings.NewReader(`{}`)),
	} {
		request.Header.Set("Authorization", "Bearer read-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestReplanProposalAcceptIsExactAndIdempotent(t *testing.T) {
	repository := replanFixture()
	server := ownerTestServer(t, repository)
	response, created := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", disruptionBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body)
	}
	hash := created["content_hash"].(string)
	if repository.proposalSaves != 0 {
		t.Fatal("creating a proposal must not save a plan")
	}

	// An "approved" flag in the body is ignored; the hash must match.
	response, _ = serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/accept", `{"content_hash":"other","approved":true}`)
	if response.Code != http.StatusBadRequest && response.Code != http.StatusConflict {
		t.Fatalf("unknown field or wrong hash status = %d", response.Code)
	}
	response, _ = serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/accept", `{"content_hash":"other"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("wrong hash status = %d", response.Code)
	}

	for range 2 {
		response, accepted := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/accept", `{"content_hash":"`+hash+`"}`)
		if response.Code != http.StatusOK || accepted["status"] != store.ProposalAccepted {
			t.Fatalf("accept status = %d: %s", response.Code, response.Body)
		}
	}
	if repository.proposalSaves != 1 {
		t.Fatalf("saves = %d, want exactly one plan version", repository.proposalSaves)
	}
	if string(repository.proposalProvenance) != string(ownerDecisionProvenance) {
		t.Fatalf("provenance = %s", repository.proposalProvenance)
	}
	response, _ = serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/reject", `{}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("reject after accept status = %d", response.Code)
	}
}

func TestReplanProposalStaleAndRejectRules(t *testing.T) {
	repository := replanFixture()
	server := ownerTestServer(t, repository)
	_, first := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", disruptionBody)
	_, second := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", disruptionBody)
	serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/accept", `{"content_hash":"`+first["content_hash"].(string)+`"}`)

	response, payload := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/2/accept", `{"content_hash":"`+second["content_hash"].(string)+`"}`)
	if response.Code != http.StatusConflict || payload["error"].(map[string]any)["code"] != "proposal_stale" {
		t.Fatalf("stale accept status = %d: %s", response.Code, response.Body)
	}
	if repository.proposalSaves != 1 {
		t.Fatalf("stale proposal saved a plan")
	}

	repository = replanFixture()
	server = ownerTestServer(t, repository)
	serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", disruptionBody)
	for range 2 {
		response, rejected := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals/1/reject", `{"note":"keep the plan"}`)
		if response.Code != http.StatusOK || rejected["status"] != store.ProposalRejected {
			t.Fatalf("reject status = %d: %s", response.Code, response.Body)
		}
	}
	if repository.proposalSaves != 0 || repository.plan.VersionID != 10 {
		t.Fatal("rejecting changed the plan")
	}
}

func TestReplanProposalValidation(t *testing.T) {
	server := ownerTestServer(t, replanFixture())
	cases := map[string]int{
		`{"base_plan_version_id":10,"reason":"late","disruption":{"start":"2026-09-25T06:15:00Z"}}`:                                                                    http.StatusBadRequest,
		`{"base_plan_version_id":9,"reason":"late","disruption":{"start":"2026-09-25T06:15:00Z","available_until":"2026-09-25T15:00:00Z"}}`:                            http.StatusConflict,
		`{"base_plan_version_id":10,"reason":"","disruption":{"start":"2026-09-25T06:15:00Z","available_until":"2026-09-25T15:00:00Z"}}`:                               http.StatusBadRequest,
		`{"base_plan_version_id":10,"reason":"edit","plan":{"blocks":[{"source_block_id":77,"title":"x","start_time":"2026-09-25T06:00:00Z","duration_minutes":30}]}}`: http.StatusBadRequest,
	}
	for body, want := range cases {
		response, _ := serve(t, server, http.MethodPost, "/v1/plans/2026-09-25/replan-proposals", body)
		if response.Code != want {
			t.Fatalf("%s status = %d, want %d: %s", body, response.Code, want, response.Body)
		}
	}
	response, _ := serve(t, server, http.MethodGet, "/v1/plans/2026-09-25/replan-proposals/5", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing proposal status = %d", response.Code)
	}
}
