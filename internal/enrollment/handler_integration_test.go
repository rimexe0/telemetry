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
	if w := call("GET", base+"/details", "other", "", ""); w.Code != 401 {
		t.Fatalf("details leaked %d", w.Code)
	}
	if w := call("GET", "/v1/devices", "other", "", ""); w.Code != 401 {
		t.Fatalf("device list leaked %d", w.Code)
	}
	if w := call("POST", base+"/approve", "owner", "https://panel.example:8443", "{}"); w.Code != 200 {
		t.Fatalf("approve %d %s", w.Code, w.Body.String())
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
