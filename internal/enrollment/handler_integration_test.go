package enrollment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPairingHTTPApprovalBoundary(t *testing.T) {
	address := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	s, err := Open(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	id, err := randomID(16)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(s, HandlerConfig{PublicURL: "https://panel.example:8443", VerifyOwner: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner" }})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	secret := "long-test-collector-credential"
	payload, _ := json.Marshal(map[string]string{"device_id": id, "device_name": "HTTP test device", "credential_hash": CredentialHash(secret)})
	start := call("POST", "/v1/pairing/sessions", "", "", string(payload))
	if start.Code != 201 {
		t.Fatalf("start %d %s", start.Code, start.Body.String())
	}
	var session struct {
		PairingID string `json:"pairing_id"`
		PanelURL  string `json:"panel_url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.PanelURL, "https://panel.example:8443/panel/?pair=") {
		t.Fatal(session.PanelURL)
	}
	base := "/v1/pairing/sessions/" + session.PairingID
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"other", "https://panel.example:8443", 401}, {"owner", "https://attacker.example", 403}, {"owner", "", 403}} {
		w := call("POST", base+"/approve", tc.token, tc.origin, "{}")
		if w.Code != tc.want {
			t.Fatalf("approval %v got %d", tc, w.Code)
		}
	}
	if w := call("GET", "/v1/pairing/sessions", "other", "", ""); w.Code != 401 {
		t.Fatalf("pending list leaked %d", w.Code)
	}
	if w := call("GET", "/v1/pairing/sessions", "owner", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), session.PairingID) {
		t.Fatalf("pending list %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", base+"/details", "other", "", ""); w.Code != 401 {
		t.Fatalf("details leaked %d", w.Code)
	}
	if w := call("GET", "/v1/devices", "other", "", ""); w.Code != 401 {
		t.Fatalf("device list leaked %d", w.Code)
	}
	if w := call("POST", base+"/approve", "owner", "https://panel.example:8443", "{}"); w.Code != 200 {
		t.Fatalf("approve %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/v1/pairing/sessions", "owner", "", ""); strings.Contains(w.Body.String(), session.PairingID) {
		t.Fatal("approved request still listed as pending")
	}
	if w := call("GET", base, secret, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"approved"`) {
		t.Fatalf("poll %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "/v1/devices/"+id+"/revoke", "other", "https://panel.example:8443", "{}"); w.Code != 401 {
		t.Fatal("other account revoked device")
	}
	if w := call("POST", "/v1/devices/"+id+"/revoke", "owner", "https://panel.example:8443", "{}"); w.Code != 200 {
		t.Fatalf("revoke %d", w.Code)
	}
}

func TestSetupCodeHTTP(t *testing.T) {
	address := os.Getenv("TELEMETRY_TEST_DATABASE_URL")
	if address == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	s, err := Open(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(s, HandlerConfig{PublicURL: "https://panel.example:8443", RelayKey: "BRELAYKEY", VerifyOwner: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer owner" }})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const origin = "https://panel.example:8443"
	if w := call("POST", "/v1/pairing/invites", "other", origin, "{}"); w.Code != 401 {
		t.Fatalf("invite without owner %d", w.Code)
	}
	if w := call("POST", "/v1/pairing/invites", "owner", "https://attacker.example", "{}"); w.Code != 403 {
		t.Fatalf("invite from another origin %d", w.Code)
	}
	if w := call("PUT", "/v1/pairing/settings", "owner", origin, `{"relay_url":"https://relay.example"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"relay_key":"BRELAYKEY"`) {
		t.Fatalf("relay settings %d %s", w.Code, w.Body.String())
	}
	w := call("POST", "/v1/pairing/invites", "owner", origin, "{}")
	if w.Code != 201 {
		t.Fatalf("invite %d %s", w.Code, w.Body.String())
	}
	var code SetupCode
	if err := json.Unmarshal(w.Body.Bytes(), &code); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code.JoinURL, DefaultJoinPage+"#") || !strings.Contains(code.JoinURL, "r=https%3A%2F%2Frelay.example") || !strings.HasPrefix(code.SetupLink, "telemetry://setup?") || !strings.HasPrefix(code.QR, "data:image/png;base64,") || !strings.Contains(code.Commands["mac"], "'"+code.JoinURL+"'") {
		t.Fatalf("setup code %+v", code)
	}
	id, _ := randomID(16)
	secret := "code-http-device-secret"
	payload, _ := json.Marshal(map[string]string{"device_id": id, "device_name": "Code device", "credential_hash": CredentialHash(secret), "code": code.Code})
	start := call("POST", "/v1/pairing/sessions", "", "", string(payload))
	if start.Code != 201 || !strings.Contains(start.Body.String(), `"status":"approved"`) {
		t.Fatalf("start with code %d %s", start.Code, start.Body.String())
	}
	payload, _ = json.Marshal(map[string]string{"device_id": id + "x", "device_name": "Second", "credential_hash": CredentialHash("x"), "code": code.Code})
	if w := call("POST", "/v1/pairing/sessions", "", "", string(payload)); w.Code != 403 || !strings.Contains(w.Body.String(), "invalid_code") {
		t.Fatalf("reused code %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/v1/pairing/invites", "owner", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"used_by_device":"`+id+`"`) || strings.Contains(w.Body.String(), code.Code) {
		t.Fatalf("invite list %d %s", w.Code, w.Body.String())
	}
}
