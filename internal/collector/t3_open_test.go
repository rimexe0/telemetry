package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rimexe0/telemetry/internal/queue"
)

// fakeT3Server answers server.getBackgroundPolicy with whatever answer()
// returns, after checking the session token and, like T3, the orchestration
// protocol version (2 unless another is given).
func fakeT3Server(t *testing.T, answer func(id string) string, protocol ...string) (string, string) {
	required := "2"
	if len(protocol) > 0 {
		required = protocol[0]
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("orchestrationProtocol") != required {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUpgradeRequired)
			w.Write([]byte(`{"code":"orchestration_protocol_incompatible","message":"Update this client.","orchestrationProtocolVersion":` + required + `}`))
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var request struct {
				ID  string `json:"id"`
				Tag string `json:"tag"`
			}
			json.Unmarshal(data, &request)
			if request.Tag != "server.getBackgroundPolicy" {
				return
			}
			// Unrelated traffic first, as a real connection may send.
			conn.Write(r.Context(), websocket.MessageText, []byte(`{"_tag":"Chunk","requestId":"x","values":[]}`))
			conn.Write(r.Context(), websocket.MessageText, []byte(answer(request.ID)))
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	port := server.URL[strings.LastIndex(server.URL, ":")+1:]
	os.WriteFile(filepath.Join(dir, "server-runtime.json"), []byte(`{"port":`+port+`}`), 0o600)
	os.WriteFile(filepath.Join(dir, "environment-id"), []byte("env-1\n"), 0o600)
	token := filepath.Join(t.TempDir(), "t3-session-token")
	os.WriteFile(token, []byte("secret\n"), 0o600)
	return dir, token
}

func lease(client, kind string, visible bool, cwd string) string {
	scopes := `[{"type":"provider-status"}]`
	if cwd != "" {
		scopes = `[{"type":"provider-status"},{"type":"vcs-status","cwd":"` + cwd + `"}]`
	}
	return `{"sessionId":"s","rpcClientId":0,"clientId":"` + client + `","clientKind":"` + kind + `","visible":` + map[bool]string{true: "true", false: "false"}[visible] +
		`,"focused":true,"recentlyInteracted":true,"scopes":` + scopes + `,"updatedAt":"2026-10-02T14:03:02.579Z","expiresAt":"2026-10-02T14:03:47.579Z"}`
}

func TestT3OpenRecordsWhichProjectEachClientShows(t *testing.T) {
	leases := []string{lease("mac", "desktop-renderer", true, "/Users/ri/code/personal/Telemetry"), lease("phone", "mobile", false, "/x")}
	dir, token := fakeT3Server(t, func(id string) string {
		return `{"_tag":"Exit","requestId":"` + id + `","exit":{"_tag":"Success","value":{"hostPower":{},"leases":[` + strings.Join(leases, ",") + `]}}}`
	})
	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac-1"}
	module := &T3Open{Dir: dir, TokenFile: token, spans: map[string]*t3OpenSpan{}}
	defer module.close()
	at := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	claim := func() []map[string]any {
		items, err := q.Claim(context.Background(), 100, time.Minute, at)
		if err != nil {
			t.Fatal(err)
		}
		found := []map[string]any{}
		for _, item := range items {
			var payload map[string]any
			json.Unmarshal(item.Observation.OriginalPayload, &payload)
			payload["_start"], payload["_end"], payload["_kind"] = item.Observation.OccurredAt, *item.Observation.EndedAt, item.Observation.Kind
			found = append(found, payload)
			q.Acknowledge(context.Background(), item, at)
		}
		return found
	}
	if state, err := module.poll(context.Background(), host, at); err != nil || state != "2 T3 clients" {
		t.Fatalf("poll = %q, %v", state, err)
	}
	first := claim()
	// The hidden phone is not recorded; the Mac's folder is.
	if len(first) != 1 || first[0]["cwd"] != "/Users/ri/code/personal/Telemetry" || first[0]["client_kind"] != "desktop-renderer" || first[0]["_kind"] != "t3.open" {
		t.Fatalf("first = %+v", first)
	}
	// Fifteen seconds later nothing changed: nothing is sent again yet.
	at = at.Add(15 * time.Second)
	module.poll(context.Background(), host, at)
	if again := claim(); len(again) != 0 {
		t.Fatalf("unchanged = %+v", again)
	}
	// A switch to another project, without any message: the first span ends
	// and a new one starts.
	leases[0] = lease("mac", "desktop-renderer", true, "/home/rime/code/work/acme")
	at = at.Add(15 * time.Second)
	module.poll(context.Background(), host, at)
	switched := claim()
	if len(switched) != 2 {
		t.Fatalf("switch = %+v", switched)
	}
	for _, span := range switched {
		switch span["cwd"] {
		case "/Users/ri/code/personal/Telemetry":
			if !span["_end"].(time.Time).Equal(at) {
				t.Fatalf("the old span must end at the switch: %+v", span)
			}
		case "/home/rime/code/work/acme":
			if !span["_start"].(time.Time).Equal(at) {
				t.Fatalf("the new span must start at the switch: %+v", span)
			}
		default:
			t.Fatalf("unexpected %+v", span)
		}
	}
}

func TestT3OpenReportsAChangedProtocol(t *testing.T) {
	for name, answer := range map[string]string{
		"leases renamed":  `{"_tag":"Exit","requestId":"1","exit":{"_tag":"Success","value":{"clients":[]}}}`,
		"method gone":     `{"_tag":"Exit","requestId":"1","exit":{"_tag":"Failure","cause":{"_tag":"Die","defect":"unknown tag"}}}`,
		"field renamed":   `{"_tag":"Exit","requestId":"1","exit":{"_tag":"Success","value":{"leases":[{"id":"mac","kind":"desktop-renderer"}]}}}`,
		"not json at all": `<html>`,
	} {
		_, done, err := parseT3Policy([]byte(answer), "1")
		if err == nil || (!done && name != "not json at all") {
			t.Fatalf("%s: done=%v err=%v", name, done, err)
		}
	}
	if _, done, err := parseT3Policy([]byte(`{"_tag":"Exit","requestId":"7","exit":{"_tag":"Success","value":{"leases":[]}}}`), "1"); done || err != nil {
		t.Fatal("an answer to another request is skipped")
	}
}

// T3 answers 426 with the protocol version it wants after an update; the
// module follows it instead of failing.
func TestT3OpenFollowsANewProtocolVersion(t *testing.T) {
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	dir, token := fakeT3Server(t, func(id string) string {
		return `{"_tag":"Exit","requestId":"` + id + `","exit":{"_tag":"Success","value":{"leases":[]}}}`
	}, "3")
	module := &T3Open{Dir: dir, TokenFile: token, spans: map[string]*t3OpenSpan{}}
	if state, err := module.poll(context.Background(), Host{Queue: q, DeviceID: "mac-1"}, time.Now()); err != nil || state != "0 T3 clients" || module.protocol != "3" {
		t.Fatalf("poll = %q, %v, protocol %s", state, err, module.protocol)
	}
}

func TestT3OpenWithoutAServerIsNotAFailure(t *testing.T) {
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "environment-id"), []byte("env-1"), 0o600)
	module := &T3Open{Dir: dir, TokenFile: filepath.Join(dir, "missing"), spans: map[string]*t3OpenSpan{}}
	if state, err := module.poll(context.Background(), Host{Queue: q, DeviceID: "mac-1"}, time.Now()); err != nil || state != "T3 is not running" {
		t.Fatalf("poll = %q, %v", state, err)
	}
	// A wrong token is a failure: the owner has to issue a new one.
	dir2, token := fakeT3Server(t, func(string) string { return "" })
	os.WriteFile(token, []byte("wrong"), 0o600)
	module = &T3Open{Dir: dir2, TokenFile: token, spans: map[string]*t3OpenSpan{}}
	if _, err := module.poll(context.Background(), Host{Queue: q, DeviceID: "mac-1"}, time.Now()); err == nil || !strings.Contains(err.Error(), "refused the session token") {
		t.Fatalf("wrong token = %v", err)
	}
}
