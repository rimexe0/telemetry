package collector

import (
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/policy"
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
	if !blocker.SiteDecision("x.com").Blocked || blocker.SiteDecision("server.example.ts.net").Blocked || blocker.SiteDecision("localhost").Blocked {
		t.Fatal("a gate blocks sites but never the Telemetry panel or localhost")
	}
}
