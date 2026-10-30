package collector

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/policy"
)

// Browsers that can run the Telemetry extension (Chromium-based) and those
// that cannot. A browser in neither list is not a browser to the blocker.
var (
	extensionBrowsers = map[string]string{
		"net.imput.helium": "Helium", "com.google.Chrome": "Chrome", "com.google.Chrome.canary": "Chrome Canary",
		"org.chromium.Chromium": "Chromium", "com.brave.Browser": "Brave", "com.microsoft.edgemac": "Edge",
		"company.thebrowser.Browser": "Arc", "com.vivaldi.Vivaldi": "Vivaldi", "com.operasoftware.Opera": "Opera",
	}
	otherBrowsers = map[string]string{
		"com.apple.Safari": "Safari", "com.apple.SafariTechnologyPreview": "Safari Technology Preview",
		"org.mozilla.firefox": "Firefox", "app.zen-browser.zen": "Zen", "com.kagi.kagimacOS": "Orion",
		"com.duckduckgo.macos.browser": "DuckDuckGo",
	}
	// extensionsPageTitles are the Extensions page's tab title in the owner's
	// languages; that page stays usable so the extension can be switched
	// back on.
	extensionsPageTitles = []string{"Extensions", "Uzantılar"}
)

// extensionsPage reports whether a browser window shows its Extensions page.
// Chromium browsers put the tab title first and then their own parts, such
// as "Extensions - Helium - You" or "Extensions - Part of group work -
// Helium - You"; the Telemetry extension's own details page adds its name,
// "Extensions - Telemetry - Helium - You". While loading the title is the
// address. A web page whose title only starts with "Extensions" is followed
// by its site's name instead and does not count.
func extensionsPage(title, browser string) bool {
	title = strings.TrimSpace(title)
	if strings.HasPrefix(title, "chrome://extensions") {
		return true
	}
	for _, page := range extensionsPageTitles {
		rest, found := strings.CutPrefix(title, page)
		if !found {
			continue
		}
		rest = strings.TrimPrefix(rest, " - Telemetry")
		if rest == "" || strings.HasPrefix(rest, " - "+browser) || strings.HasPrefix(rest, " - Part of group ") || strings.HasPrefix(rest, " - Audio playing") {
			return true
		}
	}
	return false
}

// extensionState is what the blocker knows about each browser's extension.
// It is guarded by the blocker's mutex.
type extensionState struct {
	seen      map[string]time.Time // bundle ID → last check-in
	incognito map[string]bool      // bundle ID → may run in private windows
	ports     map[string]string    // remote address → bundle ID
	since     map[int]time.Time    // browser process → first seen in front
	titles    map[int]string       // browser process → last window title that was not empty
	lastError string               // why the last check-in's app could not be found
	opened    map[string]time.Time // bundle ID → last time its Extensions page was opened
}

func (s *extensionState) init() {
	if s.seen == nil {
		*s = extensionState{seen: map[string]time.Time{}, incognito: map[string]bool{}, ports: map[string]string{},
			since: map[int]time.Time{}, opened: map[string]time.Time{}, titles: map[int]string{}}
	}
}

// ExtensionCheckIn records a request from the extension and the browser it
// came from.
func (b *Blocker) ExtensionCheckIn(remote string, incognito *bool) {
	b.mu.Lock()
	b.extensions.init()
	bundle, known := b.extensions.ports[remote]
	b.mu.Unlock()
	if !known {
		find := b.AppOfConnection
		if find == nil {
			find = desktop.AppOfConnection
		}
		var err error
		if bundle, err = find(remote); err != nil {
			b.mu.Lock()
			b.extensions.lastError = err.Error()
			b.mu.Unlock()
			return
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.extensions.ports) > 200 {
		b.extensions.ports = map[string]string{}
	}
	b.extensions.ports[remote] = bundle
	b.extensions.seen[bundle] = time.Now()
	if incognito != nil {
		b.extensions.incognito[bundle] = *incognito
	}
}

// hideBrowser hides the browser in front when the decision asks for a
// working extension and this browser has none: never seen, quiet longer
// than the decision allows, or not allowed in private windows. A browser
// first seen in front gets that long to start its extension. It reports
// whether it hid the browser.
func (b *Blocker) hideBrowser(ctx context.Context, host Host, window desktop.Window, decision policy.Decision, now time.Time) bool {
	limit := time.Duration(decision.RequireExtensionSeconds) * time.Second
	name, canRun := extensionBrowsers[window.BundleID]
	if !canRun {
		name = otherBrowsers[window.BundleID]
	}
	if limit == 0 || name == "" {
		return false
	}
	b.mu.Lock()
	b.extensions.init()
	// A menu or dropdown opened from a page has no title; it belongs to the
	// page shown just before.
	title := window.Title
	if strings.TrimSpace(title) == "" {
		title = b.extensions.titles[window.PID]
	} else {
		if len(b.extensions.titles) > 100 {
			b.extensions.titles = map[int]string{}
		}
		b.extensions.titles[window.PID] = title
	}
	if extensionsPage(title, name) {
		b.mu.Unlock()
		return false
	}
	first, ok := b.extensions.since[window.PID]
	if !ok {
		if len(b.extensions.since) > 100 {
			b.extensions.since = map[int]time.Time{}
		}
		first = now
		b.extensions.since[window.PID] = now
	}
	seen := b.extensions.seen[window.BundleID]
	incognito, said := b.extensions.incognito[window.BundleID]
	opened := b.extensions.opened[window.BundleID]
	b.mu.Unlock()
	if now.Sub(first) < limit {
		return false
	}
	running := !seen.IsZero() && now.Sub(seen) < limit
	if canRun && running && said && incognito {
		return false
	}
	reason := name + " cannot run the Telemetry extension, so it stays hidden while sites are blocked. Use Helium."
	switch {
	case canRun && running && (!said || !incognito):
		reason = "Allow the Telemetry extension in private windows: on " + name + "'s Extensions page, open Telemetry's Details and turn on Allow in Incognito. Its Extensions page is opening."
	case canRun:
		reason = "The Telemetry extension is not running in " + name + ". On " + name + "'s Extensions page, which is opening, turn it on and set its Site access to On all sites; " + name + " comes back once it runs."
	}
	hide := b.Hide
	if hide == nil {
		hide = desktop.BlockApp
	}
	hide(name+" is hidden while sites are blocked", reason, window.PID)
	if canRun && now.Sub(opened) >= time.Minute {
		b.mu.Lock()
		b.extensions.opened[window.BundleID] = now
		b.mu.Unlock()
		open := b.OpenExtensions
		if open == nil {
			open = openExtensionsPage
		}
		if err := open(window.BundleID); err != nil {
			b.health.Failed(now, err)
		}
	}
	key := fmt.Sprintf("browser:%s:%d", window.BundleID, now.Unix()/60)
	b.mu.Lock()
	b.blocking = name
	record := key != b.lastBlock
	b.lastBlock = key
	b.mu.Unlock()
	if record {
		b.record(ctx, host, key, window, decision, now)
	}
	return true
}

// openExtensionsPage brings the browser to the front on its Extensions page.
func openExtensionsPage(bundleID string) error {
	if out, err := exec.Command("/usr/bin/open", "-b", bundleID, "chrome://extensions/").CombinedOutput(); err != nil {
		return fmt.Errorf("open extensions page: %v: %s", err, out)
	}
	return nil
}
