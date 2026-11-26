package awserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBudgetCallsGoThroughTheCollectorButNeverFromWebPages(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(Handler{
		Budget: func() any { return map[string]any{"connected": true, "budget": map[string]any{"left": 107.5}} },
		Device: func(_ context.Context, path string, body []byte) (int, []byte) {
			calls = append(calls, path+" "+string(body))
			return http.StatusConflict, []byte(`{"error":"cooling_down","message":"The next door opens at 15:30."}`)
		},
	})
	defer server.Close()
	request := func(method, path, origin string) (int, string) {
		r, _ := http.NewRequest(method, server.URL+path, strings.NewReader(`{}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, body := request(http.MethodGet, "/api/telemetry/budget", "chrome-extension://"+ExtensionID); status != http.StatusOK || !strings.Contains(body, `"left":107.5`) {
		t.Fatalf("budget %d: %s", status, body)
	}
	status, body := request(http.MethodPost, "/api/telemetry/device/door", "chrome-extension://"+ExtensionID)
	if status != http.StatusConflict || !strings.Contains(body, "15:30") || len(calls) != 1 || calls[0] != "door {}" {
		t.Fatalf("door %d: %s, calls %v", status, body, calls)
	}
	if status, _ := request(http.MethodPost, "/api/telemetry/device/door", "https://example.com"); status != http.StatusForbidden || len(calls) != 1 {
		t.Fatalf("a web page opened a door: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/telemetry/device/door", ""); status == http.StatusConflict {
		t.Fatal("a GET made a call")
	}
}
