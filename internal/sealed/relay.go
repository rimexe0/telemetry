package sealed

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Relay is the server's door for sealed requests. It opens each one, checks
// it is fresh and not seen before, and passes it to Next as an ordinary
// request, but only for the paths a device uses: the panel, MCP and owner
// API cannot be reached through it.
type Relay struct {
	Key  *ServerKey
	Next http.Handler
	// MaxBytes caps a sealed request; Window is how far a request's time
	// may be from the server's.
	MaxBytes int64
	Window   time.Duration
	// PerMinute is how many requests one address may make in a minute.
	PerMinute int

	mu      sync.Mutex
	seen    map[string]time.Time
	buckets map[string]*bucket
	now     func() time.Time
}

// Paths a device may reach through the relay.
var allowed = []string{"/v1/ingest/batches", "/v1/device/"}

// Headers passed on from inside the seal; any other is dropped.
var passed = []string{"Authorization", "X-Telemetry-Device-ID", "Content-Type", "Accept"}

type bucket struct {
	tokens float64
	at     time.Time
}

func (r *Relay) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *Relay) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	now := r.clock()
	// The tunnel is the only way in, so its client address is trusted.
	address := request.Header.Get("CF-Connecting-IP")
	if address == "" {
		address = request.RemoteAddr
	}
	if !r.allow(address, now) {
		http.Error(writer, "too many requests", http.StatusTooManyRequests)
		return
	}
	message, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, r.MaxBytes))
	if err != nil {
		http.Error(writer, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	opened, err := r.Key.Open(message)
	if err != nil {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	head := opened.Head
	at := time.UnixMilli(head.At)
	if at.Before(now.Add(-r.Window)) || at.After(now.Add(r.Window)) || len(head.ID) < 16 {
		http.Error(writer, "stale", http.StatusForbidden)
		return
	}
	if !r.first(head.ID, now) {
		http.Error(writer, "replayed", http.StatusConflict)
		return
	}
	target, err := url.ParseRequestURI(head.Path)
	if err != nil || target.Host != "" || !reachable(target.Path) {
		r.answer(writer, opened, AnswerHead{Status: http.StatusNotFound}, nil)
		return
	}
	inner, err := http.NewRequestWithContext(request.Context(), head.Method, target.RequestURI(), bytes.NewReader(opened.Body))
	if err != nil {
		r.answer(writer, opened, AnswerHead{Status: http.StatusBadRequest}, nil)
		return
	}
	for _, name := range passed {
		if value, ok := head.Headers[name]; ok {
			inner.Header.Set(name, value)
		}
	}
	inner.RemoteAddr = address
	recorder := &recorder{header: http.Header{}, status: http.StatusOK}
	r.Next.ServeHTTP(recorder, inner)
	r.answer(writer, opened, AnswerHead{Status: recorder.status, ContentType: recorder.header.Get("Content-Type")}, recorder.body.Bytes())
}

// pairingPath is starting a pairing or asking how one stands, so a phone
// pairs without Tailscale. Approving stays with the owner's signed-in panel.
func pairingPath(path string) bool {
	const sessions = "/v1/pairing/sessions"
	if path == sessions {
		return true
	}
	id, ok := strings.CutPrefix(path, sessions+"/")
	return ok && id != "" && !strings.Contains(id, "/")
}

func reachable(path string) bool {
	if strings.Contains(path, "..") {
		return false
	}
	if pairingPath(path) {
		return true
	}
	for _, prefix := range allowed {
		if path == prefix || (strings.HasSuffix(prefix, "/") && strings.HasPrefix(path, prefix)) {
			return true
		}
	}
	return false
}

// answer is always 200 on the outside: the real status is inside the seal.
func (r *Relay) answer(writer http.ResponseWriter, opened *Opened, head AnswerHead, body []byte) {
	sealed, err := opened.Answer(head, body)
	if err != nil {
		log.Printf("sealed relay: %v", err)
		http.Error(writer, "internal error", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Write(sealed)
}

// first records a request id and reports whether it is new. Ids are kept
// for twice the window, longer than a request's time can be accepted.
func (r *Relay) first(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]time.Time{}
	}
	for key, at := range r.seen {
		if now.Sub(at) > 2*r.Window {
			delete(r.seen, key)
		}
	}
	if _, ok := r.seen[id]; ok {
		return false
	}
	r.seen[id] = now
	return true
}

// allow is a token bucket per address: PerMinute requests a minute, with
// up to that many at once.
func (r *Relay) allow(address string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buckets == nil {
		r.buckets = map[string]*bucket{}
	}
	limit := float64(r.PerMinute)
	b := r.buckets[address]
	if b == nil {
		if len(r.buckets) > 10000 {
			r.buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: limit, at: now}
		r.buckets[address] = b
	}
	b.tokens = min(limit, b.tokens+now.Sub(b.at).Minutes()*limit)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type recorder struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}

func (r *recorder) Write(data []byte) (int, error) {
	r.wrote = true
	return r.body.Write(data)
}
