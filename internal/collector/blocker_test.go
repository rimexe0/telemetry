package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/policy"
	"github.com/rimexe0/telemetry/internal/queue"
)

func TestBlocksLabelledAppsButNeverEssentialsOrSystem(t *testing.T) {
	work := policy.Decision{Mode: policy.ModeWork, Blocked: policy.Targets{MacApps: []string{"osu!"}}, AlwaysAllowed: policy.Targets{MacApps: []string{"Telegram"}}}
	cases := []struct {
		window desktop.Window
		want   bool
	}{
		{desktop.Window{App: "osu!", BundleID: "sh.ppy.osu"}, true},
		{desktop.Window{App: "OSU!"}, true},
		{desktop.Window{App: "T3 Code (Nightly)", BundleID: "com.t3tools.t3code"}, false},
	}
	for _, c := range cases {
		if got := Blocks(work, c.window); got != c.want {
			t.Fatalf("%+v = %v", c.window, got)
		}
	}
	gate := policy.Decision{Mode: policy.ModeGate, BlockAll: true, AlwaysAllowed: policy.Targets{MacApps: []string{"Telegram"}}}
	if !Blocks(gate, desktop.Window{App: "Helium"}) || Blocks(gate, desktop.Window{App: "Telegram"}) || Blocks(gate, desktop.Window{App: "Finder", BundleID: "com.apple.finder"}) {
		t.Fatal("a gate blocks everything except essentials and system apps")
	}
}

func TestDecisionAtFollowsTheTimelineAndToleratesASlowClock(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	timeline := []policy.Segment{
		{From: start, To: start.Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeWork}},
		{From: start.Add(time.Hour), To: start.Add(2 * time.Hour), Decision: policy.Decision{Mode: policy.ModeLeisure}},
	}
	if d := DecisionAt(timeline, start.Add(90*time.Minute)); d == nil || d.Mode != policy.ModeLeisure {
		t.Fatalf("got %+v", d)
	}
	if d := DecisionAt(timeline, start.Add(-time.Minute)); d == nil || d.Mode != policy.ModeWork {
		t.Fatal("a clock a minute behind must still use the first segment")
	}
	if DecisionAt(timeline, start.Add(-time.Hour)) != nil || DecisionAt(timeline, start.Add(3*time.Hour)) != nil {
		t.Fatal("outside the timeline nothing is decided, so nothing is blocked")
	}
}

func TestSitesFollowDomainsAndSubdomains(t *testing.T) {
	work := policy.Decision{Mode: policy.ModeWork, Blocked: policy.Targets{Domains: []string{"x.com", "youtube.com"}}, AlwaysAllowed: policy.Targets{Domains: []string{"music.youtube.com"}}}
	for host, want := range map[string]bool{"x.com": true, "www.x.com": true, "m.youtube.com": true, "music.youtube.com": false, "github.com": false, "notx.com": false} {
		if got := BlocksSite(work, host); got != want {
			t.Fatalf("%s = %v, want %v", host, got, want)
		}
	}
	blocker := &Blocker{Credentials: &Credentials{BaseURL: "https://server.example.ts.net:8443"}}
	blocker.timeline = []policy.Segment{{From: time.Now().Add(-time.Minute), To: time.Now().Add(time.Hour), Decision: policy.Decision{Mode: policy.ModeGate, BlockAll: true, Reason: "Go outside first."}}}
	if !blocker.SiteDecision("x.com", "").Blocked || blocker.SiteDecision("server.example.ts.net", "").Blocked || blocker.SiteDecision("localhost", "").Blocked {
		t.Fatal("a gate blocks sites but never the Telemetry panel or localhost")
	}
	// Closed by default with one stream kept open: that page loads, the rest of the site does not.
	blocker.timeline[0].Decision = policy.Decision{Mode: policy.ModeClosed, Reason: "Distractions are closed.",
		Blocked: policy.Targets{Domains: []string{"youtube.com"}}, OpenAddresses: []string{"youtube.com/watch?v=rFZHOHl-L8A"}}
	if blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/watch?v=rFZHOHl-L8A").Blocked ||
		!blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/watch?v=other").Blocked || !blocker.SiteDecision("www.youtube.com", "").Blocked {
		t.Fatal("only the open address stays open")
	}
}

// A change on the server reaches the Mac through the waiting request, long
// before the next minute refresh.
func TestBlockerAppliesAChangeAsSoonAsItComes(t *testing.T) {
	answer := func(fingerprint, mode string) string {
		return `{"fingerprint":"` + fingerprint + `","timeline":[{"from":"2026-01-01T00:00:00Z","to":"2099-01-01T00:00:00Z","decision":{"mode":"` + mode + `"}}]}`
	}
	changed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("wait") {
		case "":
			w.Write([]byte(answer("a", "leisure")))
		case "a":
			select {
			case <-changed:
				w.Write([]byte(answer("b", "closed")))
			case <-time.After(2 * time.Second):
				w.WriteHeader(http.StatusNotModified)
			}
		default:
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	q, _ := queue.Open(filepath.Join(t.TempDir(), "q.sqlite3"))
	defer q.Close()
	host := Host{Queue: q, DeviceID: "mac"}
	blocker := &Blocker{Credentials: &Credentials{BaseURL: server.URL, DeviceID: "mac", Secret: "s"}, HTTP: &http.Client{Timeout: 15 * time.Second}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocker.refresh(ctx, host)
	go blocker.watch(ctx, host)
	mode := func() string {
		blocker.mu.Lock()
		defer blocker.mu.Unlock()
		return DecisionAt(blocker.timeline, time.Now()).Mode
	}
	if mode() != "leisure" {
		t.Fatalf("first = %s", mode())
	}
	started := time.Now()
	close(changed)
	for mode() != "closed" {
		if time.Since(started) > time.Second {
			t.Fatal("the change did not arrive within a second")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDistractionsOnlyNeverBlocksEverything(t *testing.T) {
	now := time.Now().UTC()
	decision := policy.Decision{Mode: policy.ModeMorning, BlockAll: true, Blocked: policy.Targets{Domains: []string{"x.com"}}}
	body, _ := json.Marshal(map[string]any{"timeline": []policy.Segment{{From: now.Add(-time.Minute), To: now.Add(time.Hour), Decision: decision}}})
	for _, only := range []bool{false, true} {
		blocker := &Blocker{DistractionsOnly: only}
		if err := blocker.load(body, now); err != nil {
			t.Fatal(err)
		}
		if !blocker.SiteDecision("x.com", "x.com/home").Blocked {
			t.Fatalf("distractions only %v: a distraction was not blocked", only)
		}
		if blocked := blocker.SiteDecision("github.com", "github.com/rimexe0").Blocked; blocked == only {
			t.Fatalf("distractions only %v: github.com blocked = %v", only, blocked)
		}
	}
}
