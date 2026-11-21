package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalOwnerCannotUseLegacyPanelLogin(t *testing.T) {
	s, err := New(&fakeRepository{}, Config{IngestToken: "ingest", ReadToken: "read", PanelToken: "legacy", PanelSessionSecret: "secret", OwnerAuthorized: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner" }})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/sources", "/v1/observations", "/v1/collection/health"} {
		for _, token := range []string{"", "other-user", "owner"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if token != "owner" && w.Code != 401 {
				t.Fatalf("%s %q admitted: %d", path, token, w.Code)
			}
			if token == "owner" && w.Code != 200 {
				t.Fatalf("owner %s failed: %d", path, w.Code)
			}
		}
	}
	r := httptest.NewRequest("POST", "/v1/panel/session", strings.NewReader(`{"token":"legacy"}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 404 || len(w.Result().Cookies()) != 0 {
		t.Fatal("legacy login remained enabled with external auth")
	}
}

func TestEnrolledDeviceCannotFallBackToLegacyCredentials(t *testing.T) {
	s, err := New(&fakeRepository{}, Config{IngestToken: "global", ReadToken: "read", IngestDeviceTokens: map[string]string{"mac": "old-device"}, DeviceAuthorized: func(ctx context.Context, id, token string) (bool, bool, error) {
		return id == "mac", token == "paired", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		header, token string
		allowed       bool
	}{{"mac", "paired", true}, {"", "paired", false}, {"mac", "old-device", false}, {"", "global", false}, {"other", "paired", false}} {
		r := httptest.NewRequest("POST", "/v1/ingest/batches", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-Telemetry-Device-ID", tc.header)
		if got := s.ingestAuthorized(r, "mac"); got != tc.allowed {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
	s.config.DeviceAuthorized = func(context.Context, string, string) (bool, bool, error) { return true, false, nil }
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", "Bearer paired")
	r.Header.Set("X-Telemetry-Device-ID", "mac")
	if s.ingestAuthorized(r, "mac") {
		t.Fatal("revoked credential admitted")
	}
	s.config.DeviceAuthorized = func(context.Context, string, string) (bool, bool, error) {
		return false, false, errors.New("database offline")
	}
	r.Header.Set("Authorization", "Bearer global")
	r.Header.Del("X-Telemetry-Device-ID")
	if s.ingestAuthorized(r, "mac") {
		t.Fatal("registry failure fell back to global credential")
	}
}

// Without an external sign-in the panel token signs the owner in: the panel
// session reaches owner endpoints, the read token and no session do not.
func TestPanelTokenSessionIsTheOwnerWithoutExternalAuth(t *testing.T) {
	s, err := New(&fakeRepository{}, Config{IngestToken: "ingest", ReadToken: "read", PanelToken: "panel", PanelSessionSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	s.ServeHTTP(login, httptest.NewRequest("POST", "/v1/panel/session", strings.NewReader(`{"token":"panel"}`)))
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	request := func(authorize func(*http.Request)) int {
		r := httptest.NewRequest("GET", "/v1/activities", nil)
		authorize(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if code := request(func(r *http.Request) { r.AddCookie(cookie) }); code != 200 {
		t.Fatalf("panel owner got %d", code)
	}
	if code := request(func(r *http.Request) { r.Header.Set("Authorization", "Bearer read") }); code != 401 {
		t.Fatalf("read token reached an owner endpoint: %d", code)
	}
	if code := request(func(*http.Request) {}); code != 401 {
		t.Fatalf("anonymous reached an owner endpoint: %d", code)
	}
	forged := *cookie
	forged.Value = "9999999999.forged"
	if code := request(func(r *http.Request) { r.AddCookie(&forged) }); code != 401 {
		t.Fatalf("forged session reached an owner endpoint: %d", code)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookie)
	if !s.PanelOwner(r) || s.PanelOwner(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("PanelOwner disagrees with the session")
	}
}

// With an external sign-in the panel token is no owner, even with a
// correctly signed cookie.
func TestPanelSessionIsNoOwnerWithExternalAuth(t *testing.T) {
	plain, err := New(&fakeRepository{}, Config{IngestToken: "ingest", ReadToken: "read", PanelToken: "panel", PanelSessionSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	plain.ServeHTTP(login, httptest.NewRequest("POST", "/v1/panel/session", strings.NewReader(`{"token":"panel"}`)))
	cookie := login.Result().Cookies()[0]
	external, err := New(&fakeRepository{}, Config{IngestToken: "ingest", ReadToken: "read", PanelToken: "panel", PanelSessionSecret: "secret", OwnerAuthorized: func(*http.Request) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/activities", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	external.ServeHTTP(w, r)
	if w.Code != 401 || external.PanelOwner(r) {
		t.Fatalf("panel cookie was an owner beside external auth: %d", w.Code)
	}
}
