package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Agents hold the read token; it must never apply or remove a label.
func TestLabelDecisionsRequireOwner(t *testing.T) {
	server := testServer(t, &fakeRepository{})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/activity-labels", nil),
		httptest.NewRequest(http.MethodGet, "/v1/activity/summary", nil),
		httptest.NewRequest(http.MethodPost, "/v1/activity-label-proposals/1/accept", strings.NewReader(`{"content_hash":"x"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/activity-label-proposals/1/reject", strings.NewReader(`{"content_hash":"x"}`)),
		httptest.NewRequest(http.MethodDelete, "/v1/activity-labels/1", nil),
	} {
		request.Header.Set("Authorization", "Bearer read-secret")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.Method, request.URL.Path, response.Code)
		}
	}
}
