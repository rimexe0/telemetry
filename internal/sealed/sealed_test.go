package sealed

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func newID() string {
	raw := make([]byte, 16)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}

func relay(t *testing.T, now time.Time) (*Relay, *Sealer, *[]*http.Request) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sealed.key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || again.Public() != key.Public() {
		t.Fatalf("the key is kept: %v", err)
	}
	public, err := ParsePublic(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	seen := []*http.Request{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		seen = append(seen, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write(append([]byte(`{"got":`), append(body, '}')...))
	})
	return &Relay{Key: key, Next: next, MaxBytes: 1 << 20, Window: 5 * time.Minute, PerMinute: 3, now: func() time.Time { return now }}, NewSealer(public), &seen
}

func send(t *testing.T, r *Relay, message []byte, address string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(message))
	request.Header.Set("CF-Connecting-IP", address)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func TestRelayPassesASealedDeviceRequestAndSealsTheAnswer(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	r, sealer, seen := relay(t, now)
	head := Head{Method: "POST", Path: "/v1/ingest/batches?x=1", At: now.UnixMilli(), ID: newID(),
		Headers: map[string]string{"Authorization": "Bearer device-secret", "X-Telemetry-Device-ID": "phone", "Cookie": "no"}}
	message, open, err := sealer.Seal(head, []byte(`[1,2]`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(message, []byte("device-secret")) || bytes.Contains(message, []byte("/v1/ingest")) {
		t.Fatal("the token and path must not be readable on the way")
	}
	response := send(t, r, message, "1.2.3.4")
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("got")) {
		t.Fatalf("outside = %d %q", response.Code, response.Body.String())
	}
	answer, body, err := open(response.Body.Bytes())
	if err != nil || answer.Status != http.StatusAccepted || answer.ContentType != "application/json" || string(body) != `{"got":[1,2]}` {
		t.Fatalf("answer = %+v %q %v", answer, body, err)
	}
	inner := (*seen)[0]
	if inner.URL.Path != "/v1/ingest/batches" || inner.URL.RawQuery != "x=1" || inner.Header.Get("Authorization") != "Bearer device-secret" ||
		inner.Header.Get("X-Telemetry-Device-ID") != "phone" || inner.Header.Get("Cookie") != "" {
		t.Fatalf("inner = %s %v", inner.URL, inner.Header)
	}
	// Someone who copied the request cannot open the answer or send it again.
	if response := send(t, r, message, "1.2.3.4"); response.Code != http.StatusConflict {
		t.Fatalf("replay = %d", response.Code)
	}
}

func TestRelayRefusesWhatIsNotADeviceRequest(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	r, sealer, seen := relay(t, now)
	r.PerMinute = 100
	for _, path := range []string{"/mcp", "/panel/", "/v1/devices", "/v1/plans/2026-10-06", "/v1/device/../../mcp", "/v1/device/%2e%2e/mcp", "//evil/v1/device/now"} {
		message, open, _ := sealer.Seal(Head{Method: "GET", Path: path, At: now.UnixMilli(), ID: newID()}, nil)
		answer, _, err := open(send(t, r, message, "1.2.3.4").Body.Bytes())
		if err != nil || answer.Status != http.StatusNotFound {
			t.Fatalf("%s: %+v %v", path, answer, err)
		}
	}
	if len(*seen) != 0 {
		t.Fatal("nothing outside the device paths is reached")
	}
	stale, _, _ := sealer.Seal(Head{Method: "GET", Path: "/v1/device/now", At: now.Add(-10 * time.Minute).UnixMilli(), ID: newID()}, nil)
	if code := send(t, r, stale, "1.2.3.4").Code; code != http.StatusForbidden {
		t.Fatalf("stale = %d", code)
	}
	message, _, _ := sealer.Seal(Head{Method: "GET", Path: "/v1/device/now", At: now.UnixMilli(), ID: newID()}, nil)
	message[len(message)-1] ^= 1
	if code := send(t, r, message, "1.2.3.4").Code; code != http.StatusBadRequest {
		t.Fatalf("tampered = %d", code)
	}
	if code := send(t, r, []byte("GET / HTTP/1.1"), "1.2.3.4").Code; code != http.StatusBadRequest {
		t.Fatalf("plain = %d", code)
	}
	other, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "other.key"))
	public, _ := ParsePublic(other.Public())
	wrong, _, _ := NewSealer(public).Seal(Head{Method: "GET", Path: "/v1/device/now", At: now.UnixMilli(), ID: newID()}, nil)
	if code := send(t, r, wrong, "1.2.3.4").Code; code != http.StatusBadRequest {
		t.Fatalf("sealed to another key = %d", code)
	}
	if code := send(t, r, bytes.Repeat([]byte{1}, 2<<20), "1.2.3.4").Code; code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large = %d", code)
	}
}

func TestRelayLimitsRequestsPerAddress(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	r, _, _ := relay(t, now)
	for i := 0; i < 3; i++ {
		if code := send(t, r, []byte("x"), "1.2.3.4").Code; code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited too early", i)
		}
	}
	if code := send(t, r, []byte("x"), "1.2.3.4").Code; code != http.StatusTooManyRequests {
		t.Fatalf("fourth = %d", code)
	}
	if code := send(t, r, []byte("x"), "5.6.7.8").Code; code == http.StatusTooManyRequests {
		t.Fatal("another address has its own limit")
	}
	r.now = func() time.Time { return now.Add(time.Minute) }
	if code := send(t, r, []byte("x"), "1.2.3.4").Code; code == http.StatusTooManyRequests {
		t.Fatal("the limit refills")
	}
}
