package awserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rimexe0/telemetry/internal/changes"
)

func TestEventsTellExtensionsOnly(t *testing.T) {
	feed := changes.New()
	checkIns := make(chan struct{}, 10)
	server := httptest.NewServer(Handler{Changes: feed.Next, ExtensionVersion: func() string { return "0.6.0" },
		CheckIn: func(string, *bool) { checkIns <- struct{}{} }})
	defer server.Close()
	address := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/telemetry/events"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A web page must not connect, and neither may anything without an origin.
	for _, origin := range []string{"https://evil.example", ""} {
		headers := http.Header{}
		if origin != "" {
			headers.Set("Origin", origin)
		}
		_, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: headers})
		if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q: err = %v, response = %+v", origin, err, response)
		}
	}

	conn, _, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"chrome-extension://abc"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func() map[string]any {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		message := map[string]any{}
		json.Unmarshal(data, &message)
		return message
	}
	if hello := read(); hello["type"] != "hello" || hello["extension_version"] != "0.6.0" {
		t.Fatalf("hello = %v", hello)
	}
	feed.Changed()
	if message := read(); message["type"] != "changed" {
		t.Fatalf("message = %v", message)
	}
	feed.Changed()
	if message := read(); message["type"] != "changed" {
		t.Fatalf("second message = %v", message)
	}
	// The extension's keepalive counts as a check-in, after the one at connect.
	<-checkIns
	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-checkIns:
	case <-ctx.Done():
		t.Fatal("the keepalive did not check in")
	}
}
