package sealed

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTransportSealsServerRequestsAndFallsBack(t *testing.T) {
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "relay.key"))
	public, _ := ParsePublic(key.Public())
	// The server's handlers, reached through the relay or directly.
	via := ""
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			via = name
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"auth":"` + r.Header.Get("Authorization") + `","body":"` + string(body) + `"}`))
		})
	}
	direct := httptest.NewServer(handler("direct"))
	defer direct.Close()
	relayed := handler("relay")
	relay := httptest.NewServer(&Relay{Key: key, Next: relayed, MaxBytes: 1 << 20, Window: 5 * time.Minute, PerMinute: 100})
	server, _ := url.Parse(direct.URL)
	transport := &Transport{Server: server, Relay: relay.URL, Sealer: NewSealer(public)}
	client := &http.Client{Transport: transport}
	post := func(target string) (int, string) {
		request, _ := http.NewRequest(http.MethodPost, target, strings.NewReader("rows"))
		request.Header.Set("Authorization", "Bearer device")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if code, body := post(direct.URL + "/v1/ingest/batches"); code != http.StatusAccepted || body != `{"auth":"Bearer device","body":"rows"}` || via != "relay" {
		t.Fatalf("sealed = %d %s via %s", code, body, via)
	}
	// Paths the relay does not pass on go directly.
	if post(direct.URL + "/v1/policy/current"); via != "direct" {
		t.Fatalf("other path via %s", via)
	}
	// With the relay gone, the same request goes directly.
	relay.Close()
	if code, body := post(direct.URL + "/v1/device/now"); code != http.StatusAccepted || body != `{"auth":"Bearer device","body":"rows"}` || via != "direct" {
		t.Fatalf("fallback = %d %s via %s", code, body, via)
	}
}
