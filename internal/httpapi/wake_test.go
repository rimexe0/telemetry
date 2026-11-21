package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWakeTimeEndpoint(t *testing.T) {
	s, err := New(&fakeRepository{}, Config{IngestToken: "ingest", ReadToken: "read", OwnerAuthorized: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner" }})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/activity/wake"+query, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
	if w := call("owner", "?at=yesterday"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad time = %d", w.Code)
	}
	// No records: no quiet stretch, so no wake time.
	for _, token := range []string{"owner", "read"} {
		w := call(token, "?at=2026-11-20T15:00:00Z")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"wake_at":null`) || !strings.Contains(w.Body.String(), `"at":"2026-11-20T15:00:00Z"`) {
			t.Fatalf("%s = %d %s", token, w.Code, w.Body.String())
		}
	}
}
