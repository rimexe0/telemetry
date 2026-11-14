package collector

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/rimexe0/telemetry/internal/policy"
)

// Trimmed pages (owner decision 2026-11-14): while YouTube or Reddit is
// closed by work, focus hours or an urgent task, a few of their pages stay
// usable with the feeds taken out instead of being covered. The extension
// hides the feeds; every other page of the site stays covered. Bedtime, the
// morning and plan-your-day locks, go-outside and recovery keep the whole
// site covered.
var trimModes = map[string]bool{policy.ModeWork: true, policy.ModeBreak: true, policy.ModeClosed: true, policy.ModeUrgent: true}

// redditPost matches a single post page.
var redditPost = regexp.MustCompile(`^/((r|u|user)/[^/]+/)?comments/[^/]+`)

// TrimmedPage names the trim the extension applies to address, a page of a
// blocked site, or "" when the page stays covered.
func TrimmedPage(decision policy.Decision, host, address string) string {
	if decision.BlockAll || !trimModes[decision.Mode] {
		return ""
	}
	parsed, err := url.Parse(address)
	if err != nil || !strings.EqualFold(parsed.Hostname(), host) {
		return ""
	}
	path := parsed.EscapedPath()
	switch strings.TrimPrefix(strings.ToLower(host), "www.") {
	case "youtube.com":
		// The home page shows only the search bar; search results and
		// videos show without suggestions.
		if path == "" || path == "/" || path == "/results" || path == "/watch" || path == "/playlist" || strings.HasPrefix(path, "/live/") {
			return "youtube"
		}
	case "reddit.com":
		if redditPost.MatchString(path) {
			return "reddit"
		}
	}
	return ""
}
