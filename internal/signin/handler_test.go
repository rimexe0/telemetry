package signin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T, m *Manager) *Handler {
	t.Helper()
	h, err := NewHandler(HandlerConfig{Manager: m, PublicURL: testPublicURL, PanelTokenConfigured: true, VerifyOwner: func(r *http.Request) bool {
		return r.Header.Get("Cookie") == "owner=1"
	}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type call struct {
	method, path, body, origin string
	owner                      bool
	contentType                string
}

func (c call) do(h http.Handler) *httptest.ResponseRecorder {
	r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	if c.owner {
		r.Header.Set("Cookie", "owner=1")
	}
	if c.contentType != "-" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func TestHandlerBoundaries(t *testing.T) {
	clerk := newFakeClerk(t)
	m, _ := newTestManager(t, clerk)
	h := newTestHandler(t, m)
	keyBody := `{"publishable_key":"` + testKey + `"}`
	for _, tc := range []struct {
		name   string
		call   call
		status int
		code   string
	}{
		{"read unauthenticated", call{method: "GET", path: "/v1/settings/sign-in"}, 401, "unauthorized"},
		{"write unauthenticated", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: keyBody, origin: testPublicURL}, 401, "unauthorized"},
		{"write without origin", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: keyBody, owner: true}, 403, "untrusted_origin"},
		{"write from another origin", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: keyBody, owner: true, origin: "https://attacker.example"}, 403, "untrusted_origin"},
		{"write as a form", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: keyBody, owner: true, origin: testPublicURL, contentType: "-"}, 415, "invalid_content_type"},
		{"unknown field", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: `{"publishable_key":"x","issuer":"https://attacker.example"}`, owner: true, origin: testPublicURL}, 400, "invalid_json"},
		{"bad key", call{method: "PUT", path: "/v1/settings/sign-in/clerk", body: `{"publishable_key":"sk_test_secret"}`, owner: true, origin: testPublicURL}, 409, "invalid_settings"},
		{"missing method", call{method: "PUT", path: "/v1/settings/sign-in", body: `{"panel_token_enabled":false}`, owner: true, origin: testPublicURL}, 400, "invalid_request"},
		{"everything off", call{method: "PUT", path: "/v1/settings/sign-in", body: `{"panel_token_enabled":false,"clerk_enabled":false}`, owner: true, origin: testPublicURL}, 409, "invalid_settings"},
		{"Clerk on unlinked", call{method: "PUT", path: "/v1/settings/sign-in", body: `{"panel_token_enabled":true,"clerk_enabled":true}`, owner: true, origin: testPublicURL}, 409, "invalid_settings"},
		{"link before a key", call{method: "POST", path: "/v1/settings/sign-in/clerk/link", body: `{"session_token":"x"}`, owner: true, origin: testPublicURL}, 409, "invalid_settings"},
	} {
		w := tc.call.do(h)
		if w.Code != tc.status || errorCode(t, w) != tc.code {
			t.Errorf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	if settings := m.Settings(); settings != Defaults() {
		t.Fatalf("refused requests changed settings: %+v", settings)
	}
}

func TestHandlerLinksAndTurnsOnClerk(t *testing.T) {
	clerk := newFakeClerk(t)
	m, _ := newTestManager(t, clerk)
	h := newTestHandler(t, m)
	write := func(method, path, body string) *httptest.ResponseRecorder {
		return call{method: method, path: path, body: body, owner: true, origin: testPublicURL}.do(h)
	}
	if w := write("PUT", "/v1/settings/sign-in/clerk", `{"publishable_key":"`+testKey+`"}`); w.Code != 200 {
		t.Fatalf("set key: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ name, token string }{
		{"wrong azp", clerk.token(t, "user_owner", "https://attacker.example")},
		{"no azp", clerk.token(t, "user_owner", "")},
		{"garbage", "not-a-jwt"},
	} {
		body, _ := json.Marshal(map[string]string{"session_token": tc.token})
		if w := write("POST", "/v1/settings/sign-in/clerk/link", string(body)); w.Code != 422 || errorCode(t, w) != "clerk_token_rejected" {
			t.Errorf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	body, _ := json.Marshal(map[string]string{"session_token": clerk.token(t, "user_owner", testPublicURL)})
	w := write("POST", "/v1/settings/sign-in/clerk/link", string(body))
	var view View
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || !view.Clerk.Linked || view.Clerk.OwnerUserID != "user_owner" || view.Clerk.Enabled {
		t.Fatalf("link: %d %s", w.Code, w.Body.String())
	}
	w = write("PUT", "/v1/settings/sign-in", `{"panel_token_enabled":true,"clerk_enabled":true}`)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || !view.Clerk.Enabled || !view.PanelToken.Enabled || view.ManagedBy != "settings" || !view.CanChange {
		t.Fatalf("turn on: %d %s", w.Code, w.Body.String())
	}
	if len(view.Changes) != 3 || !strings.Contains(view.Changes[1].Change, "user_owner linked") {
		t.Fatalf("changes %+v", view.Changes)
	}
	if strings.Contains(w.Body.String(), "sk_") {
		t.Fatal("response mentions a secret key")
	}
}

// With Clerk set in the environment the page is read-only and writes refuse.
func TestEnvironmentConfigIsReadOnly(t *testing.T) {
	h, err := NewHandler(HandlerConfig{Environment: &Environment{PublishableKey: testKey, Issuer: testIssuer, OwnerLinked: true}, PublicURL: testPublicURL, VerifyOwner: func(r *http.Request) bool { return r.Header.Get("Cookie") == "owner=1" }})
	if err != nil {
		t.Fatal(err)
	}
	w := call{method: "GET", path: "/v1/settings/sign-in", owner: true}.do(h)
	var view View
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.ManagedBy != "environment" || view.CanChange || !view.Clerk.Enabled || view.PanelToken.Enabled || view.Blocked != "Sign-in is set by this server's configuration." {
		t.Fatalf("view: %d %s", w.Code, w.Body.String())
	}
	for _, c := range []call{
		{method: "PUT", path: "/v1/settings/sign-in", body: `{"panel_token_enabled":true,"clerk_enabled":false}`},
		{method: "PUT", path: "/v1/settings/sign-in/clerk", body: `{"publishable_key":"` + testKey + `"}`},
		{method: "POST", path: "/v1/settings/sign-in/clerk/unlink", body: `{}`},
	} {
		c.owner, c.origin = true, testPublicURL
		if w := c.do(h); w.Code != 409 || errorCode(t, w) != "managed_by_environment" {
			t.Errorf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

func TestWritesNeedPublicURL(t *testing.T) {
	m, err := NewManager(&memoryStore{}, ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(HandlerConfig{Manager: m, VerifyOwner: func(*http.Request) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if w := (call{method: "PUT", path: "/v1/settings/sign-in", body: `{"panel_token_enabled":true,"clerk_enabled":false}`, origin: testPublicURL}).do(h); w.Code != 409 || errorCode(t, w) != "public_url_missing" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
