package collector

import (
	"net/url"
	"testing"
	"time"

	"github.com/rimexe0/telemetry/internal/policy"
)

func TestTrimmedPagesOfYouTubeAndReddit(t *testing.T) {
	work := policy.Decision{Mode: policy.ModeWork, Blocked: policy.Targets{Domains: []string{"youtube.com", "reddit.com", "x.com"}}}
	for address, want := range map[string]string{
		"https://www.youtube.com/":                           "youtube",
		"https://www.youtube.com/results?search_query=a":     "youtube",
		"https://www.youtube.com/watch?v=abc&list=PL1":       "youtube",
		"https://www.youtube.com/playlist?list=PL1":          "youtube",
		"https://www.youtube.com/shorts/abc":                 "",
		"https://www.youtube.com/feed/subscriptions":         "",
		"https://www.youtube.com/@someone":                   "",
		"https://m.youtube.com/watch?v=abc":                  "",
		"https://www.reddit.com/r/learnart/comments/1abc/x/": "reddit",
		"https://www.reddit.com/comments/1abc":               "reddit",
		"https://www.reddit.com/user/me/comments/1abc/x/":    "reddit",
		"https://www.reddit.com/":                            "",
		"https://www.reddit.com/r/learnart/":                 "",
		"https://www.reddit.com/user/me/saved/":              "",
		"https://www.reddit.com/search/?q=a":                 "",
		"https://old.reddit.com/r/learnart/comments/1abc/x/": "",
		"https://x.com/home":                                 "",
		"":                                                   "",
	} {
		parsed, _ := url.Parse(address)
		if got := TrimmedPage(work, parsed.Hostname(), address); got != want {
			t.Errorf("%s = %q, want %q", address, got, want)
		}
	}
	for _, locked := range []policy.Decision{{Mode: policy.ModeBedtime}, {Mode: policy.ModeMorning}, {Mode: policy.ModePlanGate}, {Mode: policy.ModeGate},
		{Mode: policy.ModeOutside}, {Mode: policy.ModeRecovery}, {Mode: policy.ModeWork, BlockAll: true}} {
		if TrimmedPage(locked, "www.youtube.com", "https://www.youtube.com/watch?v=abc") != "" {
			t.Errorf("%s (lock all %v) must cover the whole site", locked.Mode, locked.BlockAll)
		}
	}
	if TrimmedPage(work, "www.youtube.com", "https://evil.example/watch") != "" {
		t.Error("the address must belong to the host asked about")
	}

	// The site stays blocked, so an extension that knows no trims covers it.
	blocker := &Blocker{}
	blocker.timeline = []policy.Segment{{From: time.Now().Add(-time.Minute), To: time.Now().Add(time.Hour), Decision: work}}
	if d := blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/watch?v=abc"); !d.Blocked || d.Trim != "youtube" {
		t.Fatalf("got %+v", d)
	}
	if d := blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/shorts/abc"); !d.Blocked || d.Trim != "" {
		t.Fatalf("got %+v", d)
	}
}

func TestAShortOffersTheSameVideoOnTheTrimmedPage(t *testing.T) {
	work := policy.Decision{Mode: policy.ModeClosed, Blocked: policy.Targets{Domains: []string{"youtube.com"}}}
	for address, want := range map[string]string{
		"https://www.youtube.com/shorts/xpteV4r0WbU": "https://www.youtube.com/watch?v=xpteV4r0WbU",
		"https://www.youtube.com/watch/xpteV4r0WbU":  "https://www.youtube.com/watch?v=xpteV4r0WbU",
		"https://www.youtube.com/shorts/":            "",
		"https://www.youtube.com/shorts/a/b":         "",
		"https://www.youtube.com/feed/subscriptions": "",
		"https://m.youtube.com/shorts/xpteV4r0WbU":   "",
		"https://evil.example/shorts/xpteV4r0WbU":    "",
	} {
		parsed, _ := url.Parse(address)
		if got := VideoInstead(work, "www.youtube.com", address); parsed.Hostname() == "www.youtube.com" && got != want {
			t.Errorf("%s = %q, want %q", address, got, want)
		}
	}
	if got := VideoInstead(work, "m.youtube.com", "https://m.youtube.com/shorts/xpteV4r0WbU"); got != "" {
		t.Errorf("m.youtube.com = %q", got)
	}
	if got := VideoInstead(work, "www.youtube.com", "https://evil.example/shorts/xpteV4r0WbU"); got != "" {
		t.Errorf("another host = %q", got)
	}
	if got := VideoInstead(policy.Decision{Mode: policy.ModeBedtime}, "www.youtube.com", "https://www.youtube.com/shorts/xpteV4r0WbU"); got != "" {
		t.Errorf("bedtime covers the whole site, got %q", got)
	}
	blocker := &Blocker{}
	blocker.timeline = []policy.Segment{{From: time.Now().Add(-time.Minute), To: time.Now().Add(time.Hour), Decision: work}}
	if d := blocker.SiteDecision("www.youtube.com", "https://www.youtube.com/shorts/xpteV4r0WbU"); !d.Blocked || d.Trim != "" || d.Instead != "https://www.youtube.com/watch?v=xpteV4r0WbU" {
		t.Fatalf("got %+v", d)
	}
}
