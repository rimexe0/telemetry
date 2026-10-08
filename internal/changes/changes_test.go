package changes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlyChangingRequestsWakeDevices(t *testing.T) {
	feed := New()
	handler := feed.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	woke := func(method, path, body string) bool {
		next := feed.Next()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, strings.NewReader(body)))
		select {
		case <-next:
			return true
		default:
			return false
		}
	}
	for _, c := range []struct {
		method, path, body string
		want               bool
	}{
		{"GET", "/v1/device/now", "", false},
		{"POST", "/v1/device/focus", `{"action":"start"}`, true},
		{"POST", "/fail", "", false},
		{"POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_day_plan","arguments":{}}}`, true},
		{"POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"day_plan","arguments":{}}}`, false},
		{"POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, false},
		{"POST", "/v1/ingest/batches", `{"observations":[{"kind":"android.device_state"}]}`, false},
		{"POST", "/v1/ingest/batches", `{"observations":[{"kind":"android.policy_emergency"}]}`, true},
	} {
		if got := woke(c.method, c.path, c.body); got != c.want {
			t.Fatalf("%s %s %s: woke = %v", c.method, c.path, c.body, got)
		}
	}
}
