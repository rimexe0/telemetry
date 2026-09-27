package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/store"
)

func TestPolicyRoutesRequireOwner(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/policy/config", nil),
		httptest.NewRequest(http.MethodPut, "/v1/policy/config", strings.NewReader(`{"config":{}}`)),
		httptest.NewRequest(http.MethodGet, "/v1/policy/current", nil),
		httptest.NewRequest(http.MethodGet, "/v1/policy/timeline", nil),
	} {
		request.Header.Set("Authorization", "Bearer read-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestPolicyConfigAndCurrentDecision(t *testing.T) {
	activity := int64(1)
	now := time.Now().UTC()
	repository := &fakeRepository{plan: &store.Plan{PlanDate: now.Format("2006-01-02"), VersionID: 4, Blocks: []store.PlanBlock{
		{ID: 9, ActivityID: &activity, Title: "Client work", StartTime: now.Add(-30 * time.Minute), DurationMinutes: 60, State: "active"},
	}}}
	server := ownerTestServer(t, repository)

	response, payload := serve(t, server, http.MethodGet, "/v1/policy/current", "")
	decision := payload["decision"].(map[string]any)
	if response.Code != http.StatusOK || payload["configured"] != false || decision["mode"] != "work" || len(decision["blocked_groups"].([]any)) != 0 {
		t.Fatalf("unconfigured current = %d %v", response.Code, payload)
	}
	if response, _ := serve(t, server, http.MethodPut, "/v1/policy/config", `{"config":{"distractions":[{"id":"a"},{"id":"a"}]}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate ids status = %d", response.Code)
	}
	response, saved := serve(t, server, http.MethodPut, "/v1/policy/config", `{"config":{"distractions":[{"id":"social","domains":["Feed.Example.com"]}]},"note":"first list"}`)
	if response.Code != http.StatusOK || saved["policy"].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("save = %d %v", response.Code, saved)
	}
	_, payload = serve(t, server, http.MethodGet, "/v1/policy/current", "")
	decision = payload["decision"].(map[string]any)
	if decision["policy_version"].(float64) != 1 || decision["blocked"].(map[string]any)["domains"].([]any)[0] != "feed.example.com" || !strings.Contains(decision["reason"].(string), "Client work") {
		t.Fatalf("configured current = %v", decision)
	}
	_, payload = serve(t, server, http.MethodGet, "/v1/policy/timeline?hours=2", "")
	segments := payload["segments"].([]any)
	if len(segments) != 2 || segments[0].(map[string]any)["decision"].(map[string]any)["mode"] != "work" || segments[1].(map[string]any)["decision"].(map[string]any)["mode"] != "leisure" {
		t.Fatalf("timeline = %v", segments)
	}
	if response, _ := serve(t, server, http.MethodGet, "/v1/policy/timeline?hours=100", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("hours bound status = %d", response.Code)
	}
}

func TestPolicyUsesPlanGateSettingAndRecovery(t *testing.T) {
	repository := &fakeRepository{settings: store.PlanningSettings{DailyPlanGateEnabled: true}}
	repository.policies = []store.PolicyVersion{{ID: 1}}
	repository.policies[0].Config.Distractions = nil
	server := ownerTestServer(t, repository)
	_, payload := serve(t, server, http.MethodGet, "/v1/policy/current", "")
	if mode := payload["decision"].(map[string]any)["mode"]; mode != "plan_gate" {
		t.Fatalf("mode = %v", mode)
	}
	repository.plan = &store.Plan{VersionID: 1}
	repository.executionPrimary = &store.ExecutionSession{ID: 3, State: "interrupted", Events: []store.ExecutionEvent{{EventType: "offline_recovery_started"}}}
	_, payload = serve(t, server, http.MethodGet, "/v1/policy/current", "")
	if mode := payload["decision"].(map[string]any)["mode"]; mode != "recovery" {
		t.Fatalf("mode = %v", mode)
	}
}

func TestPolicySuggestionsAreOwnerOnly(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/policy/suggestions", nil)
	request.Header.Set("Authorization", "Bearer read-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	response2, payload := serve(t, ownerTestServer(t, &fakeRepository{}), http.MethodGet, "/v1/policy/suggestions", "")
	if response2.Code != http.StatusOK || len(payload["android_apps"].([]any)) != 1 {
		t.Fatalf("suggestions = %d %v", response2.Code, payload)
	}
}
