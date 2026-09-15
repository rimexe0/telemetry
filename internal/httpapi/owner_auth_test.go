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
