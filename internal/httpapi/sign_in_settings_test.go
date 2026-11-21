package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// With stored sign-in settings the panel token and Clerk work side by side,
// and turning either off applies to the next request.
func TestStoredSignInSettingsSwapAtRuntime(t *testing.T) {
	var panelOn, clerkOn atomic.Bool
	panelOn.Store(true)
	s, err := New(&fakeRepository{}, Config{
		IngestToken: "ingest", ReadToken: "read", PanelToken: "panel", PanelSessionSecret: "secret",
		PanelTokenEnabled: panelOn.Load,
		OwnerAuthorized: func(r *http.Request) bool {
			return clerkOn.Load() && r.Header.Get("Authorization") == "Bearer clerk-owner"
		},
		ClerkPublishableKeyFunc: func() string {
			if clerkOn.Load() {
				return "pk_test_key"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	authConfig := func() map[string]any {
		var body map[string]any
		_ = json.Unmarshal(serve(httptest.NewRequest("GET", "/v1/auth/config", nil)).Body.Bytes(), &body)
		return body
	}
	login := serve(httptest.NewRequest("POST", "/v1/panel/session", strings.NewReader(`{"token":"panel"}`)))
	if login.Code != 200 {
		t.Fatalf("login %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	owner := func(authorize func(*http.Request)) (activities, sources int, status string) {
		for i, path := range []string{"/v1/activities", "/v1/sources"} {
			r := httptest.NewRequest("GET", path, nil)
			authorize(r)
			if i == 0 {
				activities = serve(r).Code
			} else {
				sources = serve(r).Code
			}
		}
		r := httptest.NewRequest("GET", "/v1/panel/session", nil)
		authorize(r)
		w := serve(r)
		status = strings.TrimSpace(w.Body.String())
		return
	}
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }
	withClerk := func(r *http.Request) { r.Header.Set("Authorization", "Bearer clerk-owner") }
	if a, src, st := owner(withCookie); a != 200 || src != 200 || st != `{"authenticated":true}` {
		t.Fatalf("panel token only: %d %d %s", a, src, st)
	}
	if a, _, _ := owner(withClerk); a != 401 {
		t.Fatalf("Clerk worked while off: %d", a)
	}
	if body := authConfig(); body["publishable_key"] != "" || body["panel_token_enabled"] != true {
		t.Fatalf("auth config panel only: %v", body)
	}

	clerkOn.Store(true)
	if a, src, st := owner(withClerk); a != 200 || src != 200 || st != `{"authenticated":true}` {
		t.Fatalf("Clerk beside the panel token: %d %d %s", a, src, st)
	}
	if a, _, _ := owner(withCookie); a != 200 {
		t.Fatalf("panel token stopped beside Clerk: %d", a)
	}
	if body := authConfig(); body["publishable_key"] != "pk_test_key" || body["panel_token_enabled"] != true {
		t.Fatalf("auth config both: %v", body)
	}

	panelOn.Store(false)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookie)
	if a, src, st := owner(withCookie); a != 401 || src != 401 || st != `{"authenticated":false}` || s.PanelOwner(r) {
		t.Fatalf("panel cookie still signed in after the token was turned off: %d %d %s", a, src, st)
	}
	if w := serve(httptest.NewRequest("POST", "/v1/panel/session", strings.NewReader(`{"token":"panel"}`))); w.Code != 404 {
		t.Fatalf("panel login while off: %d", w.Code)
	}
	if w := serve(httptest.NewRequest("DELETE", "/v1/panel/session", nil)); w.Code != 200 {
		t.Fatalf("sign out while off: %d", w.Code)
	}
	if a, _, _ := owner(withClerk); a != 200 {
		t.Fatalf("Clerk only: %d", a)
	}
	if body := authConfig(); body["panel_token_enabled"] != false {
		t.Fatalf("auth config Clerk only: %v", body)
	}
}
