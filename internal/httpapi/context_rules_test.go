package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextRuleRoutesRequireOwner(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/context-rules", nil),
		httptest.NewRequest(http.MethodPost, "/v1/context-rules", strings.NewReader(`{"activity_id":1,"signal":"domain","pattern":"example.com"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/context-rules/1/disable", nil),
	} {
		request.Header.Set("Authorization", "Bearer read-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d", request.Method, request.URL.Path, response.Code)
		}
	}
}

func TestContextRuleLifecycle(t *testing.T) {
	repository := &fakeRepository{}
	server := ownerTestServer(t, repository)
	response, created := serve(t, server, http.MethodPost, "/v1/context-rules", `{"activity_id":1,"signal":"path_prefix","pattern":"/work/client","id":99,"disabled_at":"2026-01-01T00:00:00Z"}`)
	if response.Code != http.StatusCreated || created["id"].(float64) != 1 || created["disabled_at"] != nil {
		t.Fatalf("create = %d %v", response.Code, created)
	}
	for body, want := range map[string]int{
		`{"activity_id":1,"signal":"app_name","pattern":"Codex"}`:       http.StatusBadRequest,
		`{"activity_id":404,"signal":"domain","pattern":"example.com"}`: http.StatusNotFound,
	} {
		if response, _ := serve(t, server, http.MethodPost, "/v1/context-rules", body); response.Code != want {
			t.Fatalf("%s status = %d, want %d", body, response.Code, want)
		}
	}
	if response, _ := serve(t, server, http.MethodPost, "/v1/context-rules/1/disable", ""); response.Code != http.StatusOK {
		t.Fatalf("disable status = %d", response.Code)
	}
	_, listed := serve(t, server, http.MethodGet, "/v1/context-rules", "")
	if len(listed["rules"].([]any)) != 0 {
		t.Fatalf("disabled rule listed: %v", listed)
	}
	_, all := serve(t, server, http.MethodGet, "/v1/context-rules?include_disabled=true", "")
	if len(all["rules"].([]any)) != 1 {
		t.Fatalf("disabled rule not retained: %v", all)
	}
}
