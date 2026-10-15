// Package changes tells waiting devices that something affecting them has
// changed, so a new plan, label, opening, focus session or emergency reaches
// the phone and the Mac at once instead of at their next poll. It only says
// "something changed"; each device then asks for its current decision.
package changes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Feed counts changes and wakes whoever waits for the next one.
type Feed struct {
	mu   sync.Mutex
	next chan struct{}
}

func New() *Feed { return &Feed{next: make(chan struct{})} }

// Changed wakes everyone waiting.
func (f *Feed) Changed() {
	f.mu.Lock()
	close(f.next)
	f.next = make(chan struct{})
	f.mu.Unlock()
}

// Next is closed at the next change.
func (f *Feed) Next() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.next
}

// Agent tools that change what devices do.
var changingTools = map[string]bool{
	"block_now": true, "change_activity_labels": true, "change_follow_ups": true, "change_task_rules": true, "change_tasks": true,
	"focus_session": true, "open_now": true, "remember_for_task": true, "revert_change": true, "set_blocking_schedule": true, "set_day_plan": true,
}

// Observation kinds that change decisions when a device uploads them.
// Positions and the watch's walks count toward the outside rule.
var changingKinds = [][]byte{[]byte("android.policy_emergency"), []byte("watch.outside_walk"), []byte("android.location.fix")}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

// Flush passes streaming responses (MCP) through.
func (r *recorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Middleware calls Changed after a request that changed something: a
// successful write to the API, an agent tool that changes plans or
// blocking, or an upload that carries an emergency unlock. Reads and
// ordinary uploads change nothing.
func (f *Feed) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
			next.ServeHTTP(writer, request)
			return
		}
		changes := true
		switch {
		case request.URL.Path == "/mcp" || request.URL.Path == "/v1/ingest/batches":
			body, err := io.ReadAll(request.Body)
			request.Body.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
			if err != nil {
				changes = false
				break
			}
			if request.URL.Path == "/mcp" {
				changes = toolChanges(body)
			} else {
				changes = false
				for _, kind := range changingKinds {
					changes = changes || bytes.Contains(body, kind)
				}
			}
		}
		recorded := &recorder{ResponseWriter: writer}
		next.ServeHTTP(recorded, request)
		// Writing nothing is an implicit 200.
		if changes && recorded.status < 400 {
			f.Changed()
		}
	})
}

// toolChanges reports whether an MCP request calls a tool that changes
// plans or blocking.
func toolChanges(body []byte) bool {
	var call struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(bytes.TrimSpace(body), &call) != nil || call.Method != "tools/call" {
		return false
	}
	return changingTools[strings.TrimSpace(call.Params.Name)]
}
