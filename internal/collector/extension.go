package collector

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/policy"
)

// Browsers that can run the Telemetry extension (Chromium-based, and
// Firefox-based with the Firefox package) and those that cannot. A browser
// in neither list is not a browser to the blocker.
var (
	extensionBrowsers = map[string]string{
		"net.imput.helium": "Helium", "com.google.Chrome": "Chrome", "com.google.Chrome.canary": "Chrome Canary",
		"org.chromium.Chromium": "Chromium", "com.brave.Browser": "Brave", "com.microsoft.edgemac": "Edge",
		"company.thebrowser.Browser": "Arc", "com.vivaldi.Vivaldi": "Vivaldi", "com.operasoftware.Opera": "Opera",
		"net.librewolf.librewolf": "LibreWolf", "org.mozilla.firefox": "Firefox",
		// Linux: the desktop file names KWin reports.
		"helium": "Helium", "google-chrome": "Chrome", "chromium": "Chromium", "chromium-browser": "Chromium",
		"brave-browser": "Brave", "microsoft-edge": "Edge", "vivaldi-stable": "Vivaldi", "opera": "Opera",
		"librewolf": "LibreWolf", "io.gitlab.librewolf-community": "LibreWolf", "firefox": "Firefox",
	}
	otherBrowsers = map[string]string{
		"com.apple.Safari": "Safari", "com.apple.SafariTechnologyPreview": "Safari Technology Preview",
		"app.zen-browser.zen": "Zen", "com.kagi.kagimacOS": "Orion",
		"com.duckduckgo.macos.browser": "DuckDuckGo",
		// Linux: the desktop file names KWin reports.
		"zen": "Zen", "app.zen_browser.zen": "Zen", "org.gnome.Epiphany": "Web",
	}
	// firefoxBrowsers run the Firefox package: their Add-ons page, window
	// titles and wording differ from Chromium's.
	firefoxBrowsers = map[string]bool{
		"net.librewolf.librewolf": true, "org.mozilla.firefox": true,
		"librewolf": true, "io.gitlab.librewolf-community": true, "firefox": true,
	}
	// signedOnly are the Firefox browsers that install only add-ons Mozilla
	// signed; LibreWolf can be told to install an unsigned one.
	signedOnly = map[string]bool{"org.mozilla.firefox": true, "firefox": true}
	// extensionsPageTitles are the Extensions page's tab title in the owner's
	// languages; that page stays usable so the extension can be switched
	// back on. addonsPageTitles are Firefox's Add-ons page's, and its
	// about:config page's (Advanced Preferences), which stays usable too: an
	// unsigned extension may need a preference changed there before it runs
	// (owner, 2026-11-27).
	extensionsPageTitles = []string{"Extensions", "Uzantılar"}
	addonsPageTitles     = []string{"Add-ons Manager", "Eklenti Yöneticisi", "Eklentiler", "Advanced Preferences", "Gelişmiş Tercihler"}
)

// extensionsPage reports whether a browser window shows its Extensions page.
// Chromium browsers put the tab title first and then their own parts, such
// as "Extensions - Helium - You" or "Extensions - Part of group work -
// Helium - You"; the Telemetry extension's own details page adds its name,
// "Extensions - Telemetry - Helium - You". While loading the title is the
// address. A web page whose title only starts with "Extensions" is followed
// by its site's name instead and does not count.
//
// Firefox browsers show the Add-ons page's title alone on macOS and add
// " — LibreWolf" on Linux, or " — Private Browsing" in a private window.
// Their about:config page counts as the Add-ons page; while it loads its
// title is the address.
func extensionsPage(title, browser string, firefox bool) bool {
	title = strings.TrimSpace(title)
	if firefox {
		if strings.HasPrefix(title, "about:addons") {
			return true
		}
		for _, page := range append(addonsPageTitles, "about:config") {
			if rest, found := strings.CutPrefix(title, page); found &&
				(rest == "" || strings.HasPrefix(rest, " — "+browser) || strings.HasPrefix(rest, " — Private Browsing")) {
				return true
			}
		}
		return false
	}
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
	// Each browser profile runs its own copy of the extension, and each copy
	// reports the titles of the tabs shown in its windows. A window whose
	// title matches none of them belongs to a profile, or a guest window,
	// without a running extension.
	tabs      map[string]map[string]time.Time // bundle ID → tab title → last reported
	unmatched map[int]time.Time               // browser process → first seen with an unreported title
}

// tabMatchGrace is how long a browser window may show a title no extension
// has reported. A copy reports a new title within moments, so this only
// covers the time between a page changing and the report arriving.
const tabMatchGrace = 10 * time.Second

func (s *extensionState) init() {
	if s.seen == nil {
		*s = extensionState{seen: map[string]time.Time{}, incognito: map[string]bool{}, ports: map[string]string{},
			since: map[int]time.Time{}, opened: map[string]time.Time{}, titles: map[int]string{},
			tabs: map[string]map[string]time.Time{}, unmatched: map[int]time.Time{}}
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

// ExtensionTabs records the titles of the tabs one copy of the extension
// shows, which tells the profile it runs in.
func (b *Blocker) ExtensionTabs(remote string, titles []string) {
	b.ExtensionCheckIn(remote, nil)
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.extensions.init()
	bundle, known := b.extensions.ports[remote]
	if !known {
		return
	}
	reported := b.extensions.tabs[bundle]
	if reported == nil {
		reported = map[string]time.Time{}
		b.extensions.tabs[bundle] = reported
	}
	for title, at := range reported {
		if now.Sub(at) > 10*time.Minute {
			delete(reported, title)
		}
	}
	for _, title := range titles {
		if title = strings.TrimSpace(title); title != "" && len(reported) < 1000 {
			reported[title] = now
		}
	}
}

// reportedTitle reports whether a window title shows a tab some copy of the
// extension reported lately. Chromium browsers put the tab's title first,
// then their own parts, such as "GitHub - Helium - You"; Firefox browsers
// show the tab's title alone on macOS and add " — LibreWolf" on Linux.
// Called with the mutex held.
func (s *extensionState) reportedTitle(bundle, title string, within time.Duration, now time.Time) bool {
	title = strings.TrimSpace(title)
	// Developer tools and picture in picture have windows of their own but
	// no tab, as has Firefox's Library (history, bookmarks and downloads).
	if strings.HasPrefix(title, "DevTools - ") || strings.HasPrefix(title, "Picture in picture") || strings.HasPrefix(title, "Picture-in-Picture") ||
		(firefoxBrowsers[bundle] && (title == "Library" || title == "Kitaplık")) {
		return true
	}
	for tab, at := range s.tabs[bundle] {
		if now.Sub(at) < within && (title == tab || strings.HasPrefix(title, tab+" - ") || strings.HasPrefix(title, tab+" — ")) {
			return true
		}
	}
	return false
}

// hideBrowser hides the browser in front when the decision asks for a
// working extension and this browser has none: never seen, quiet longer
// than the decision allows, or not allowed in private windows. A browser
// first seen in front gets that long to start its extension. It reports
// whether it hid the browser. Once the browser's extension reports its tabs,
// a window whose tab no copy reported, such as one from another profile,
// is hidden too.
func (b *Blocker) hideBrowser(ctx context.Context, host Host, window desktop.Window, decision policy.Decision, now time.Time) bool {
	limit := time.Duration(decision.RequireExtensionSeconds) * time.Second
	name, canRun := extensionBrowsers[window.BundleID]
	if !canRun {
		name = otherBrowsers[window.BundleID]
	}
	if limit == 0 || name == "" {
		return false
	}
	firefox := firefoxBrowsers[window.BundleID]
	if signedOnly[window.BundleID] && (b.FirefoxSigned == nil || !b.FirefoxSigned()) {
		canRun = false
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
	if extensionsPage(title, name, firefox) {
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
	// Older copies of the extension do not report tabs; the check starts
	// with the first copy that does.
	reports := len(b.extensions.tabs[window.BundleID]) > 0
	matched := !reports || b.extensions.reportedTitle(window.BundleID, title, limit, now)
	if matched {
		delete(b.extensions.unmatched, window.PID)
	} else if _, ok := b.extensions.unmatched[window.PID]; !ok {
		if len(b.extensions.unmatched) > 100 {
			b.extensions.unmatched = map[int]time.Time{}
		}
		b.extensions.unmatched[window.PID] = now
	}
	unmatched := !matched && now.Sub(b.extensions.unmatched[window.PID]) >= tabMatchGrace
	b.mu.Unlock()
	if now.Sub(first) < limit {
		return false
	}
	running := !seen.IsZero() && now.Sub(seen) < limit
	if canRun && running && said && incognito && !unmatched {
		return false
	}
	reason := browserReason(name, canRun, running && said && incognito, running, firefox, runtime.GOOS == "linux")
	if signedOnly[window.BundleID] && !canRun {
		reason = name + " installs only extensions Mozilla signed, and this release's Telemetry extension is not signed, so " + name + " stays hidden while sites are blocked. Use LibreWolf or Helium."
	}
	// Linux browsers do not open their Extensions page when another program
	// asks; browser policy installs the extension there instead.
	linux := runtime.GOOS == "linux"
	hide := b.Hide
	if hide == nil {
		hide = desktop.BlockApp
	}
	hide(name+" is hidden while sites are blocked", reason, window.PID)
	if canRun && !linux && now.Sub(opened) >= time.Minute {
		b.mu.Lock()
		b.extensions.opened[window.BundleID] = now
		b.mu.Unlock()
		open := b.OpenExtensions
		if open == nil && firefox {
			open = openAddonsPage
		} else if open == nil {
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

// browserReason says why a browser is hidden and what brings it back.
// fullyRunning means its extension runs and may run in private windows, so a
// window it does not know belongs to a profile without it.
func browserReason(name string, canRun, fullyRunning, running, firefox, linux bool) string {
	switch {
	case !canRun:
		return name + " cannot run the Telemetry extension, so it stays hidden while sites are blocked. Use Helium."
	case linux && firefox:
		return "The Telemetry extension is not running in " + name + ". Restart " + name + ": the Telemetry browser policy installs it in every profile and allows it in private windows. Without the policy, run install-linux.sh once."
	case linux:
		return "The Telemetry extension is not running in " + name + ". Restart " + name + ": the Telemetry browser policy installs it in every profile. Without the policy, run install-linux.sh once. Then turn on Allow in Incognito in its Details."
	case firefox && fullyRunning:
		return "This " + name + " window's profile has no Telemetry extension running. Restart " + name + ": the Telemetry policy installs it in every profile. Or use a profile that has it."
	case firefox && running:
		return "Allow the Telemetry extension in private windows: on " + name + "'s Add-ons page, which is opening, open Telemetry and set Run in Private Windows to Allow."
	case firefox:
		return "The Telemetry extension is not running in " + name + ". On " + name + "'s Add-ons page, which is opening, turn it on; " + name + " comes back once it runs. If it is not listed, restart " + name + " while Telemetry runs: the Telemetry policy installs it from there."
	case fullyRunning:
		return "This " + name + " window's profile has no Telemetry extension running. Add it in this profile from its Extensions page, which is opening, or use a profile that has it."
	case running:
		return "Allow the Telemetry extension in private windows: on " + name + "'s Extensions page, open Telemetry's Details and turn on Allow in Incognito. Its Extensions page is opening."
	}
	return "The Telemetry extension is not running in " + name + ". On " + name + "'s Extensions page, which is opening, turn it on and set its Site access to On all sites; " + name + " comes back once it runs."
}

// openExtensionsPage brings the browser to the front on its Extensions page.
func openExtensionsPage(bundleID string) error {
	if err := desktop.OpenInApp(bundleID, "chrome://extensions/"); err != nil {
		return fmt.Errorf("open extensions page: %w", err)
	}
	return nil
}

// openAddonsPage brings a Firefox browser to the front on its Add-ons page.
func openAddonsPage(bundleID string) error {
	if err := desktop.OpenInApp(bundleID, "about:addons"); err != nil {
		return fmt.Errorf("open add-ons page: %w", err)
	}
	return nil
}
