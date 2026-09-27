package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sessionServer(t *testing.T, sessions *DeviceSessions) *Server {
	t.Helper()
	server, err := New(&fakeRepository{}, Config{
		IngestToken:     "ingest-secret",
		ReadToken:       "read-secret",
		OwnerAuthorized: func(*http.Request) bool { return false }, // no Clerk session in the web view
		DeviceAuthorized: func(_ context.Context, deviceID, token string) (bool, bool, error) {
			return deviceID == "android-1", deviceID == "android-1" && token == "device-secret", nil
		},
		DeviceSessions: sessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestPairedDeviceGetsAPanelSession(t *testing.T) {
	sessions := NewDeviceSessions([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	server := sessionServer(t, sessions)
	issue := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/device/panel-session", nil)
		request.Header.Set("X-Telemetry-Device-ID", "android-1")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	if response := issue("ingest-secret"); response.Code != http.StatusUnauthorized {
		t.Fatalf("legacy token got a session: %d", response.Code)
	}
	response := issue("device-secret")
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("issue = %d %s", response.Code, response.Body)
	}
	cookie := response.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.Name != deviceSessionCookieName {
		t.Fatalf("cookie = %+v", cookie)
	}
	get := func(path string, cookie *http.Cookie) int {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code
	}
	if code := get("/v1/activities", nil); code != http.StatusUnauthorized {
		t.Fatalf("no cookie = %d", code)
	}
	if code := get("/v1/activities", cookie); code != http.StatusOK {
		t.Fatalf("with session = %d", code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/panel/session", nil)
	request.AddCookie(cookie)
	status := httptest.NewRecorder()
	server.ServeHTTP(status, request)
	if !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatalf("panel status = %s", status.Body)
	}
	forged := *cookie
	forged.Value = strings.Replace(cookie.Value, ".", "x.", 1)
	if code := get("/v1/activities", &forged); code != http.StatusUnauthorized {
		t.Fatalf("forged session = %d", code)
	}
	sessions.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if code := get("/v1/activities", cookie); code != http.StatusUnauthorized {
		t.Fatalf("expired session = %d", code)
	}
}

func TestSessionsFromAnotherKeyAreRejected(t *testing.T) {
	value, _ := NewDeviceSessions([]byte("key-one-key-one-key-one-key-one!"), time.Hour).issue("android-1")
	other := NewDeviceSessions([]byte("key-two-key-two-key-two-key-two!"), time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: deviceSessionCookieName, Value: value})
	if other.Authorized(request) {
		t.Fatal("session from another key accepted")
	}
}
