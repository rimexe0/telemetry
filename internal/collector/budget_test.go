package collector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/queue"
)

// The contract's example budget (budget-and-habits.md).
const exampleBudget = `{"enabled": true, "day": "2026-11-26", "budget": 150, "spent": 42.5, "left": 107.5,
  "earned": 30, "penalty": 0, "debt": 0, "debt_tomorrow": 0, "borrowable": 90,
  "rate": {"total": 3, "base": 3, "grayscale_off": 0, "kind": "day"},
  "in_distraction_time": false, "next_distraction_time": "2026-11-26T16:00:00Z", "bedtime_at": "2026-11-26T21:00:00Z",
  "wind_down": false, "grayscale_declined": false, "door": null, "door_available": true, "door_unavailable_reason": "",
  "next_door_at": null, "habits": [{"id": 1, "name": "Walk", "done": true, "value": 30}], "reasons": ["Mine"],
  "settings": {"wait_minutes": 10, "window_minutes": 60, "window_use_minutes": 10, "grace_minutes": 1, "borrow_multiplier": 2}}`

func answerWith(t *testing.T, segments []policy.Segment, budget string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"fingerprint": "f", "timeline": segments, "budget": json.RawMessage(budget)})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestBudgetReachesTheExtensionAsTheServerSentIt(t *testing.T) {
	now := time.Now().UTC()
	distractions := policy.Targets{Domains: []string{"reddit.com", "www.youtube.com"}}
	segments := []policy.Segment{
		{From: now.Add(-time.Minute), To: now.Add(time.Hour), Decision: policy.Decision{Mode: modeDoor, Blocked: policy.Targets{},
			OpenAddresses: []string{"youtube.com/watch?v=open"}}},
		{From: now.Add(time.Hour), To: now.Add(2 * time.Hour), Decision: policy.Decision{Mode: policy.ModeClosed, Blocked: distractions}},
	}
	blocker := &Blocker{}
	if err := blocker.load(answerWith(t, segments, exampleBudget), now); err != nil {
		t.Fatal(err)
	}
	status := blocker.Budget()
	var budget map[string]any
	if err := json.Unmarshal(status.Budget, &budget); err != nil || budget["left"] != 107.5 || budget["reasons"].([]any)[0] != "Mine" {
		t.Fatalf("budget = %s (%v)", status.Budget, err)
	}
	if !status.Connected || status.BedtimeUntil != nil {
		t.Fatalf("status = %+v", status)
	}
	// The door has distractions open; the timeline names them all the same.
	for host, want := range map[string]bool{"old.reddit.com": true, "m.youtube.com": true, "github.com": false} {
		decision := blocker.SiteDecision(host, "https://"+host+"/")
		if decision.Blocked || decision.Distraction != want {
			t.Fatalf("%s = %+v", host, decision)
		}
	}
	if blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/watch?v=open").Distraction {
		t.Fatal("an open address is not distraction use")
	}
	// A later answer that closes nothing keeps what is known.
	segments = segments[:1]
	if err := blocker.load(answerWith(t, segments, `null`), now); err != nil {
		t.Fatal(err)
	}
	if !blocker.SiteDecision("reddit.com", "").Distraction || string(blocker.Budget().Budget) != "null" {
		t.Fatal("the distraction sites were forgotten, or the budget kept after the server stopped sending it")
	}
	// Stale answers offer no door.
	blocker.fetchedAt = now.Add(-10 * time.Minute)
	if blocker.Budget().Connected {
		t.Fatal("a ten-minute-old answer counts as connected")
	}
}

func TestBedtimeUntilJoinsTheBedtimeSegments(t *testing.T) {
	start := time.Date(2026, 11, 26, 21, 0, 0, 0, time.UTC)
	segments := []policy.Segment{
		{From: start.Add(-time.Hour), To: start, Decision: policy.Decision{Mode: policy.ModeClosed}},
		{From: start, To: start.Add(4 * time.Hour), Decision: policy.Decision{Mode: policy.ModeBedtime, LockScreen: true}},
		{From: start.Add(4 * time.Hour), To: start.Add(11 * time.Hour), Decision: policy.Decision{Mode: policy.ModeBedtime, LockScreen: true}},
		{From: start.Add(11 * time.Hour), To: start.Add(12 * time.Hour), Decision: policy.Decision{Mode: policy.ModeMorning}},
	}
	if until, ok := bedtimeUntil(segments, start.Add(time.Hour)); !ok || !until.Equal(start.Add(11*time.Hour)) {
		t.Fatalf("until = %v %v", until, ok)
	}
	if _, ok := bedtimeUntil(segments, start.Add(-time.Minute)); ok {
		t.Fatal("before bedtime there is no bedtime end")
	}
}

// A door is a decision like any other: during bedtime it lifts the screen
// lock because the decision no longer asks for one.
func TestADoorDuringBedtimeLiftsTheScreenLock(t *testing.T) {
	q, err := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac"}
	now := time.Now().UTC()
	locks := 0
	blocker := &Blocker{Lock: func() error { locks++; return nil }}
	segments := []policy.Segment{
		{From: now.Add(-time.Minute), To: now.Add(10 * time.Minute), Decision: policy.Decision{Mode: modeDoor, Reason: "Door open until 23:20."}},
		{From: now.Add(10 * time.Minute), To: now.Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeBedtime, LockScreen: true}},
	}
	if err := blocker.load(answerWith(t, segments, exampleBudget), now); err != nil {
		t.Fatal(err)
	}
	platform := &lockPlatform{idle: time.Second}
	if blocker.lockScreen(context.Background(), host, platform, now) || locks != 0 {
		t.Fatal("the screen locked during a door")
	}
	if !blocker.lockScreen(context.Background(), host, platform, now.Add(11*time.Minute)) || locks != 1 {
		t.Fatal("the screen did not lock when the door ended in bedtime")
	}
}

func TestBudgetModesNameTheBlock(t *testing.T) {
	used := policy.Decision{Mode: modeBudgetUsed, Reason: "Today's distraction time is used up."}
	if got := closedTitle("osu!", used); got != "osu! is closed: today's distraction time is used up" {
		t.Fatalf("title = %q", got)
	}
	if ModeLabel(modeDoor) != "Door open" || ModeLabel(modeBudgetUsed) != "Distraction time used up" {
		t.Fatal("the budget modes have no label")
	}
	if got := closedTitle("osu!", policy.Decision{Mode: policy.ModeClosed}); got != "osu! is closed during focus hours" {
		t.Fatalf("title = %q", got)
	}
}

// deviceServer answers like the Telemetry server and records what this
// computer asked.
type deviceServer struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
	status   int
	reply    string
	// answer is /v1/device/now's; a closed hour when nil.
	answer []byte
}

func (s *deviceServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s" || r.Header.Get("X-Telemetry-Device-ID") != "mac" {
			t.Errorf("%s %s without the device's credentials", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.bodies = append(s.bodies, string(body))
		status, reply, answer := s.status, s.reply, s.answer
		s.mu.Unlock()
		if r.URL.Path == "/v1/device/now" && answer != nil {
			w.Write(answer)
			return
		}
		if r.URL.Path == "/v1/device/now" {
			w.Write(answerWith(t, []policy.Segment{{From: time.Now().Add(-time.Minute), To: time.Now().Add(time.Hour),
				Decision: policy.Decision{Mode: policy.ModeClosed}}}, exampleBudget))
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(reply))
	})
}

func (s *deviceServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func TestTheExtensionsCallsGoToTheServerAsThisComputer(t *testing.T) {
	server := &deviceServer{status: http.StatusConflict, reply: `{"error":"cooling_down","message":"The next door opens at 15:30."}`}
	httpServer := httptest.NewServer(server.handler(t))
	defer httpServer.Close()
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac"}
	blocker := &Blocker{Credentials: &Credentials{BaseURL: httpServer.URL, DeviceID: "mac", Secret: "s"}, HTTP: httpServer.Client(), host: &host}
	ctx := context.Background()

	status, reply := blocker.DeviceCall(ctx, "door", nil)
	if status != http.StatusConflict || !strings.Contains(string(reply), "15:30") {
		t.Fatalf("409 not passed on: %d %s", status, reply)
	}
	if seen := server.seen(); len(seen) != 1 || seen[0] != "POST /v1/device/door" || server.bodies[0] != "{}" {
		t.Fatalf("requests = %v %v", seen, server.bodies)
	}
	// Anything else is refused here, without asking the server.
	for _, path := range []string{"grayscale", "door/7/open", "../policy", "habits/x/tick"} {
		if status, _ := blocker.DeviceCall(ctx, path, nil); status != http.StatusNotFound {
			t.Fatalf("%s = %d", path, status)
		}
	}
	if len(server.seen()) != 1 {
		t.Fatal("a refused call reached the server")
	}
	// A tick answers with the new budget, and the new answer is fetched.
	server.status, server.reply = http.StatusOK, strings.Replace(exampleBudget, `"left": 107.5`, `"left": 137.5`, 1)
	if status, _ := blocker.DeviceCall(ctx, "habits/2/tick", []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("tick = %d", status)
	}
	if seen := server.seen(); seen[1] != "POST /v1/device/habits/2/tick" || seen[2] != "GET /v1/device/now" {
		t.Fatalf("requests = %v", seen)
	}
	for _, path := range []string{"door/7/cancel", "door/back", "habits/2/untick"} {
		if status, _ := blocker.DeviceCall(ctx, path, nil); status != http.StatusOK {
			t.Fatalf("%s = %d", path, status)
		}
	}
	// Without the server there is no door.
	httpServer.Close()
	if status, reply := blocker.DeviceCall(ctx, "door", nil); status != http.StatusBadGateway || !strings.Contains(string(reply), "can't be reached") {
		t.Fatalf("offline = %d %s", status, reply)
	}
}

func TestGrayscaleKeptOffIsReportedAndCharged(t *testing.T) {
	server := &deviceServer{status: http.StatusOK, reply: `{}`}
	httpServer := httptest.NewServer(server.handler(t))
	defer httpServer.Close()
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac"}
	screen, sets := false, 0
	var shown []string
	choice := 0
	blocker := &Blocker{Credentials: &Credentials{BaseURL: httpServer.URL, DeviceID: "mac", Secret: "s"}, HTTP: httpServer.Client(),
		GetGrayscale: func() (bool, error) { return screen, nil },
		SetGrayscale: func(on bool) error { screen = on; sets++; return nil },
		Prompt: func(title, _ string, options []string, _ time.Duration) int {
			shown = append(shown, options...)
			return choice
		},
		async: func(f func()) { f() }}
	now := time.Now().UTC()
	windDown := []policy.Segment{{From: now.Add(-time.Minute), To: now.Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeClosed, Grayscale: true}}}
	budget := strings.Replace(exampleBudget, `"grace_minutes": 1,`, `"grace_minutes": 1, "rate_grayscale_off": 3,`, 1)
	server.answer = answerWith(t, windDown, budget)
	if err := blocker.load(server.answer, now); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	blocker.grayscale(ctx, host, now)
	if !screen || len(shown) != 0 {
		t.Fatal("the wind-down did not turn grayscale on without asking")
	}
	// Switched off: asked once, and "Keep it off" leaves it off.
	screen = false
	blocker.grayscale(ctx, host, now)
	if len(shown) != 2 || shown[0] != "Keep it off: +3× until bedtime ends" || shown[1] != "Switch back" || screen {
		t.Fatalf("notice = %v, gray = %v", shown, screen)
	}
	blocker.grayscale(ctx, host, now.Add(time.Second))
	blocker.grayscale(ctx, host, now.Add(2*time.Second))
	if len(shown) != 2 || screen {
		t.Fatal("asked again, or put back after Keep it off")
	}
	if !containsRequest(server, "POST /v1/device/grayscale", `"action":"decline"`) {
		t.Fatalf("decline not reported: %v %v", server.seen(), server.bodies)
	}
	// A restart keeps the choice.
	restarted := &Blocker{Credentials: blocker.Credentials, HTTP: blocker.HTTP, GetGrayscale: blocker.GetGrayscale, SetGrayscale: blocker.SetGrayscale,
		Prompt: blocker.Prompt, async: blocker.async}
	_ = restarted.load(answerWith(t, windDown, budget), now)
	restarted.grayscale(ctx, host, now)
	if len(shown) != 2 || screen {
		t.Fatal("a restart forgot Keep it off")
	}
	// Turned on again by the owner: the restore is reported.
	screen = true
	restarted.grayscale(ctx, host, now.Add(3*time.Second))
	restarted.grayscale(ctx, host, now.Add(4*time.Second))
	if !containsRequest(server, "POST /v1/device/grayscale", `"action":"restore"`) {
		t.Fatalf("restore not reported: %v", server.bodies)
	}
	// Off again, and this time "Switch back".
	choice = 1
	screen = false
	restarted.grayscale(ctx, host, now.Add(5*time.Second))
	if !screen || len(shown) != 4 {
		t.Fatal("Switch back did not turn grayscale on")
	}
	// After the wind-down it goes off.
	_ = restarted.load(answerWith(t, []policy.Segment{{From: now.Add(-time.Minute), To: now.Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeClosed}}}, budget), now)
	restarted.grayscale(ctx, host, now.Add(6*time.Second))
	if screen {
		t.Fatal("still gray after the wind-down")
	}
}

func TestGrayscaleNoticeNamesTheSurchargeItKnows(t *testing.T) {
	var state budgetState
	if keepGrayscaleOff(state) != "Keep it off until bedtime ends" {
		t.Fatal("without a budget the notice names a cost")
	}
	state.Enabled = true
	if got := keepGrayscaleOff(state); got != "Keep it off: distraction time costs more until bedtime ends" {
		t.Fatalf("unknown surcharge = %q", got)
	}
	state.Rate.GrayscaleOff = 2.5
	if got := keepGrayscaleOff(state); got != "Keep it off: +2.5× until bedtime ends" {
		t.Fatalf("surcharge in force = %q", got)
	}
}

func TestUnreadableGrayscaleIsUnknownAndLeftAlone(t *testing.T) {
	sets := 0
	blocker := &Blocker{GetGrayscale: func() (bool, error) { return false, desktop.ErrGrayscaleUnknown }, SetGrayscale: func(bool) error { sets++; return nil }}
	now := time.Now().UTC()
	_ = blocker.load(answerWith(t, []policy.Segment{{From: now.Add(-time.Minute), To: now.Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeClosed, Grayscale: true}}}, `null`), now)
	blocker.grayscale(context.Background(), Host{}, now)
	if sets != 0 || blocker.Status()["grayscale"] != "unknown" || blocker.Status()["error"] != nil {
		t.Fatalf("sets %d, status %v", sets, blocker.Status())
	}
}

func containsRequest(server *deviceServer, request, body string) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	for i, seen := range server.requests {
		if seen == request && strings.Contains(server.bodies[i], body) {
			return true
		}
	}
	return false
}
