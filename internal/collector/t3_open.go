package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/rimexe0/telemetry/internal/model"
)

// T3Open asks the local T3 Code server which project each of its clients
// has open. Every T3 client (desktop, web, phone) reports to its server
// whether it is visible and which project folder its open thread works in;
// the window title is the same for every thread, so this is what shows a
// switch between projects without a message being sent. It records spans of
// "this client showed this folder". Message contents are never read.
//
// It uses T3 Code's internal protocol with a session issued by
// `t3 auth session issue`. When the call fails or its answer no longer has
// the expected shape, the module reports failure, so the server raises an
// incident and tells the owner.
type T3Open struct {
	// Dir is T3's userdata directory; TokenFile holds the session token.
	Dir       string
	TokenFile string
	Interval  time.Duration

	health  Health
	mu      sync.Mutex
	state   string
	spans   map[string]*t3OpenSpan
	conn    *websocket.Conn
	request int
}

const t3OpenSchemaVersion = "t3-background-policy-1"

// t3OpenRefresh is how often an open span's end is sent again while it lasts.
const t3OpenRefresh = time.Minute

type t3OpenSpan struct {
	key, client, kind, root string
	start, end, sent        time.Time
}

func (*T3Open) Name() string { return "t3-open" }

func (t *T3Open) Status() map[string]any {
	status := t.health.Fields()
	t.mu.Lock()
	defer t.mu.Unlock()
	status["state"] = t.state
	status["open"] = len(t.spans)
	return status
}

func (t *T3Open) Run(ctx context.Context, host Host) error {
	if t.Interval == 0 {
		t.Interval = 15 * time.Second
	}
	t.spans = map[string]*t3OpenSpan{}
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	defer t.close()
	for {
		now := time.Now().UTC()
		state, err := t.poll(ctx, host, now)
		t.mu.Lock()
		t.state = state
		t.mu.Unlock()
		if err != nil {
			t.close()
			t.health.Failed(time.Now(), err)
		} else {
			t.health.Succeeded(time.Now())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (t *T3Open) close() {
	if t.conn != nil {
		t.conn.Close(websocket.StatusNormalClosure, "")
		t.conn = nil
	}
}

// poll asks once and records what changed. A T3 server that is not running
// is not a failure: nothing is open then.
func (t *T3Open) poll(ctx context.Context, host Host, now time.Time) (string, error) {
	origin, err := t3Origin(t.Dir)
	if err != nil {
		return "T3 is not running", t.record(ctx, host, now, nil)
	}
	if t.conn == nil {
		token, err := os.ReadFile(t.TokenFile)
		if err != nil {
			return "", fmt.Errorf("T3 session token: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, response, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(origin, "http")+"/ws", &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + strings.TrimSpace(string(token))}},
		})
		cancel()
		if err != nil {
			var refused *net.OpError
			if errors.As(err, &refused) {
				return "T3 is not running", t.record(ctx, host, now, nil)
			}
			if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
				return "", fmt.Errorf("T3 refused the session token (issue a new one with `t3 auth session issue`): %w", err)
			}
			return "", fmt.Errorf("connect to T3 (its protocol may have changed in an update): %w", err)
		}
		conn.SetReadLimit(4 << 20)
		t.conn = conn
	}
	t.request++
	id := strconv.Itoa(t.request)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, _ := json.Marshal(map[string]any{"_tag": "Request", "id": id, "tag": "server.getBackgroundPolicy", "payload": map[string]any{}, "headers": []any{}})
	if err := t.conn.Write(callCtx, websocket.MessageText, request); err != nil {
		return "", fmt.Errorf("ask T3: %w", err)
	}
	for {
		_, data, err := t.conn.Read(callCtx)
		if err != nil {
			return "", fmt.Errorf("read T3's answer (its protocol may have changed in an update): %w", err)
		}
		leases, done, err := parseT3Policy(data, id)
		if err != nil {
			return "", err
		}
		if done {
			return fmt.Sprintf("%d T3 clients", len(leases)), t.record(ctx, host, now, leases)
		}
	}
}

// t3Origin reads where the local T3 server listens.
func t3Origin(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "server-runtime.json"))
	if err != nil {
		return "", err
	}
	var runtime struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(data, &runtime); err != nil || runtime.Port == 0 {
		return "", fmt.Errorf("T3 server-runtime.json has no port")
	}
	return "http://127.0.0.1:" + strconv.Itoa(runtime.Port), nil
}

// T3Lease is one T3 client as its server describes it.
type T3Lease struct {
	ClientID   string `json:"clientId"`
	ClientKind string `json:"clientKind"`
	Visible    *bool  `json:"visible"`
	Focused    bool   `json:"focused"`
	Scopes     []struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	} `json:"scopes"`
}

// parseT3Policy reads one message of the T3 protocol. done is false for a
// message that answers something else. Every field the module relies on is
// checked, so a change in T3's protocol is reported instead of read as
// "nothing open".
func parseT3Policy(data []byte, id string) (leases []T3Lease, done bool, err error) {
	var message struct {
		Tag       string `json:"_tag"`
		RequestID string `json:"requestId"`
		Exit      *struct {
			Tag   string          `json:"_tag"`
			Value json.RawMessage `json:"value"`
			Cause json.RawMessage `json:"cause"`
		} `json:"exit"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		return nil, false, fmt.Errorf("T3's answer is not JSON (its protocol may have changed in an update): %w", err)
	}
	if message.Tag != "Exit" || message.RequestID != id {
		return nil, false, nil
	}
	if message.Exit == nil || message.Exit.Tag != "Success" {
		cause := ""
		if message.Exit != nil {
			cause = string(message.Exit.Cause)
		}
		if len(cause) > 300 {
			cause = cause[:300]
		}
		return nil, true, fmt.Errorf("T3 refused server.getBackgroundPolicy (its protocol may have changed in an update): %s", cause)
	}
	var value struct {
		Leases *[]T3Lease `json:"leases"`
	}
	if err := json.Unmarshal(message.Exit.Value, &value); err != nil || value.Leases == nil {
		return nil, true, errors.New("T3's client list has no leases (its protocol changed in an update)")
	}
	for _, lease := range *value.Leases {
		if lease.ClientID == "" || lease.ClientKind == "" || lease.Visible == nil {
			return nil, true, errors.New("a T3 client has no id, kind or visibility (its protocol changed in an update)")
		}
	}
	return *value.Leases, true, nil
}

// record turns the visible clients' folders into spans: a span ends when
// its client stops showing that folder.
func (t *T3Open) record(ctx context.Context, host Host, now time.Time, leases []T3Lease) error {
	current := map[string]T3Lease{}
	roots := map[string]string{}
	for _, lease := range leases {
		if !*lease.Visible {
			continue
		}
		folders := []string{}
		for _, scope := range lease.Scopes {
			if scope.Type == "vcs-status" && scope.Cwd != "" {
				folders = append(folders, scope.Cwd)
			}
		}
		if len(folders) == 0 {
			continue
		}
		sort.Strings(folders)
		key := lease.ClientID + "\x00" + strings.Join(folders, "\x00")
		current[key], roots[key] = lease, strings.Join(folders, "\n")
	}
	environment, err := os.ReadFile(filepath.Join(t.Dir, "environment-id"))
	if err != nil {
		return fmt.Errorf("T3 environment id: %w", err)
	}
	instance := "t3:" + host.DeviceID + ":" + strings.TrimSpace(string(environment))
	t.mu.Lock()
	defer t.mu.Unlock()
	send := func(span *t3OpenSpan) error {
		end := span.end
		// The end is in the payload: a revision is a change of payload.
		payload, _ := json.Marshal(map[string]any{"client_id": span.client, "client_kind": span.kind, "cwd": span.root,
			"start": span.start.Format(time.RFC3339Nano), "end": end.Format(time.RFC3339Nano)})
		observation := model.Observation{SourceInstanceKey: instance, SourceKind: "t3", DeviceID: host.DeviceID, SourceRecordKey: span.key,
			Kind: "t3.open", OccurredAt: span.start, EndedAt: &end, CollectedAt: now, SourceSchemaVersion: t3OpenSchemaVersion, OriginalPayload: payload}
		if err := observation.Normalize(); err != nil {
			return err
		}
		if _, err := host.Queue.Enqueue(ctx, observation, now); err != nil {
			return err
		}
		span.sent = now
		return nil
	}
	for key, span := range t.spans {
		if _, still := current[key]; still {
			continue
		}
		span.end = now
		if err := send(span); err != nil {
			return err
		}
		delete(t.spans, key)
	}
	for key, lease := range current {
		span := t.spans[key]
		if span == nil {
			span = &t3OpenSpan{key: fmt.Sprintf("open:%s:%d", lease.ClientID, now.UnixMilli()), client: lease.ClientID, kind: lease.ClientKind, root: roots[key], start: now}
			t.spans[key] = span
		}
		span.end = now
		if now.Sub(span.sent) >= t3OpenRefresh {
			if err := send(span); err != nil {
				return err
			}
		}
	}
	return nil
}
