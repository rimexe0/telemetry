package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rimexe0/telemetry/internal/awserver"
	"github.com/rimexe0/telemetry/internal/desktop"
	"github.com/rimexe0/telemetry/internal/model"
	"github.com/rimexe0/telemetry/internal/policy"
)

// Credentials are a paired device's server credentials.
type Credentials struct {
	BaseURL  string
	DeviceID string
	Secret   string
}

// Blocker hides apps the approved policy blocks right now and shows the
// reason in a small notice; it never covers the screen or quits an app. It follows the server's decision timeline and keeps
// following the cached one offline; without a current timeline it blocks
// nothing.
type Blocker struct {
	Credentials *Credentials
	HTTP        *http.Client
	// OnNow, when set, receives each fresh /v1/device/now answer.
	OnNow func(body []byte)

	health    Health
	mu        sync.Mutex
	timeline  []policy.Segment
	fetchedAt time.Time
	// fingerprint is the server's for the answer in use; a waiting request
	// returns when it changes.
	fingerprint string
	blocking    string
	lastBlock   string
	// host is set by Run; site checks record blocks through it.
	host      *Host
	lastSites map[string]bool
}

const (
	blockerCursor = "blocker:device_now"
	// clockTolerance lets a clock slightly behind the server's still use the
	// first segment.
	clockTolerance = 10 * time.Minute
	ownBundleID    = "cyou.rime.telemetry.desktop"
)

// macSystemSafe stay usable even when a gate blocks everything else.
var macSystemSafe = map[string]bool{
	ownBundleID: true, "com.apple.finder": true, "com.apple.systempreferences": true, "com.apple.loginwindow": true,
	"com.apple.dock": true, "com.apple.Spotlight": true, "com.apple.controlcenter": true, "com.apple.SecurityAgent": true,
	"com.apple.accessibility.universalAccessAuthWarn": true, "com.apple.ScreenSaver.Engine": true,
}

func (*Blocker) Name() string { return "blocker" }

func (b *Blocker) Status() map[string]any {
	status := b.health.Fields()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.fetchedAt.IsZero() {
		status["decisions_fetched_at"] = b.fetchedAt.UTC()
	}
	status["blocking"] = b.blocking
	status["block_screen"] = desktop.BlockScreenSupported()
	return status
}

func (b *Blocker) Run(ctx context.Context, host Host) error {
	if b.HTTP == nil {
		b.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	platform, err := desktop.NewPlatform()
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.host, b.lastSites = &host, map[string]bool{}
	b.mu.Unlock()
	if cached, ok, err := host.Queue.LoadCursor(ctx, blockerCursor); err == nil && ok {
		b.load([]byte(cached), time.Time{})
	}
	refresh := time.NewTicker(time.Minute)
	defer refresh.Stop()
	sample := time.NewTicker(time.Second)
	defer sample.Stop()
	b.refresh(ctx, host)
	// A change made by the assistant or the owner arrives through a waiting
	// request at once; the minute refresh keeps what is on screen current.
	go b.watch(ctx, host)
	for {
		select {
		case <-ctx.Done():
			desktop.HideBlock()
			return nil
		case <-refresh.C:
			b.refresh(ctx, host)
		case <-sample.C:
			window, ok, err := platform.ActiveWindow()
			if err != nil || !ok {
				continue
			}
			b.enforce(ctx, host, window, time.Now())
		}
	}
}

func (b *Blocker) refresh(ctx context.Context, host Host) {
	if b.Credentials == nil {
		b.health.Failed(time.Now(), errors.New("this computer is not paired; run telemetry-desktop login to receive blocking decisions"))
		return
	}
	body, _, err := b.fetch(ctx, "")
	if err != nil {
		b.health.Failed(time.Now(), err)
		return
	}
	b.apply(ctx, host, body)
}

// apply takes a fresh answer into use.
func (b *Blocker) apply(ctx context.Context, host Host, body []byte) {
	if err := b.load(body, time.Now()); err != nil {
		b.health.Failed(time.Now(), err)
		return
	}
	host.Queue.SaveCursor(ctx, blockerCursor, string(body), time.Now())
	b.health.Succeeded(time.Now())
	if b.OnNow != nil {
		b.OnNow(body)
	}
}

// watch keeps one request waiting for the answer to change and applies a
// change as soon as it comes. Errors back off; the minute refresh carries on.
func (b *Blocker) watch(ctx context.Context, host Host) {
	pause := 5 * time.Second
	for ctx.Err() == nil {
		b.mu.Lock()
		known := b.fingerprint
		b.mu.Unlock()
		if known == "" {
			if !sleep(ctx, pause) {
				return
			}
			continue
		}
		body, status, err := b.fetch(ctx, known)
		switch {
		case err != nil:
			if !sleep(ctx, pause) {
				return
			}
			pause = min(2*pause, time.Minute)
		case status == http.StatusOK:
			pause = 5 * time.Second
			b.apply(ctx, host, body)
		default:
			pause = 5 * time.Second
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// fetch asks for the current answer; with known set it waits up to 50
// seconds for it to differ and returns 304 with no body when it did not.
func (b *Blocker) fetch(ctx context.Context, known string) ([]byte, int, error) {
	address := strings.TrimRight(b.Credentials.BaseURL, "/") + "/v1/device/now"
	client := b.HTTP
	if known != "" {
		address += "?timeout=50&wait=" + url.QueryEscape(known)
		client = &http.Client{Transport: b.HTTP.Transport, Timeout: 75 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+b.Credentials.Secret)
	request.Header.Set("X-Telemetry-Device-ID", b.Credentials.DeviceID)
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode == http.StatusNotModified && known != "" {
		return nil, response.StatusCode, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("decisions: HTTP %d", response.StatusCode)
	}
	return body, response.StatusCode, nil
}

func (b *Blocker) load(body []byte, fetchedAt time.Time) error {
	var now struct {
		Timeline    []policy.Segment `json:"timeline"`
		Fingerprint string           `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &now); err != nil {
		return fmt.Errorf("decisions: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timeline, b.fingerprint = now.Timeline, now.Fingerprint
	if !fetchedAt.IsZero() {
		b.fetchedAt = fetchedAt
	}
	return nil
}

func (b *Blocker) enforce(ctx context.Context, host Host, window desktop.Window, now time.Time) {
	b.mu.Lock()
	decision := DecisionAt(b.timeline, now)
	b.mu.Unlock()
	if window.BundleID == ownBundleID {
		return
	}
	if decision == nil || !Blocks(*decision, window) {
		b.mu.Lock()
		b.blocking = ""
		b.mu.Unlock()
		return
	}
	// Hidden, not quit: the app keeps its state for after the block.
	desktop.BlockApp(window.App+" is closed during "+strings.ToLower(ModeLabel(decision.Mode)), decision.Reason, window.PID)
	key := fmt.Sprintf("block:%s:%d", window.App, now.Unix()/60)
	b.mu.Lock()
	b.blocking = window.App
	record := key != b.lastBlock
	b.lastBlock = key
	b.mu.Unlock()
	if record {
		b.record(ctx, host, key, window, *decision, now)
	}
}

func (b *Blocker) record(ctx context.Context, host Host, key string, window desktop.Window, decision policy.Decision, now time.Time) {
	payload, _ := json.Marshal(map[string]any{
		"app": window.App, "bundle_id": window.BundleID, "mode": decision.Mode, "reason": decision.Reason,
		"policy_version": decision.PolicyVersion, "block_screen": desktop.BlockScreenSupported(),
	})
	observation := model.Observation{
		SourceInstanceKey: "telemetry-desktop:" + host.DeviceID + ":policy", SourceKind: "desktop_policy", DeviceID: host.DeviceID,
		SourceRecordKey: key, Kind: "desktop.policy_block", OccurredAt: now.UTC(), CollectedAt: now.UTC(),
		SourceSchemaVersion: "desktop-policy-1", OriginalPayload: payload,
	}
	if observation.Normalize() == nil {
		host.Queue.Enqueue(ctx, observation, now)
	}
}

// alwaysOpenHosts stay reachable during any block: planning the day must
// stay possible while a gate is closed.
func (b *Blocker) alwaysOpen(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if b.Credentials != nil {
		if parsed, err := url.Parse(b.Credentials.BaseURL); err == nil && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}

// SiteDecision answers whether host is blocked now, for the browser
// extension. address is the page's full address when the extension sends it;
// a page among the decision's open addresses stays open. Without a current
// decision nothing is blocked.
func (b *Blocker) SiteDecision(host, address string) awserver.SiteDecision {
	b.mu.Lock()
	decision := DecisionAt(b.timeline, time.Now())
	b.mu.Unlock()
	result := awserver.SiteDecision{Host: host}
	if decision == nil || b.alwaysOpen(host) {
		return result
	}
	if BlocksSite(*decision, host) && !policy.AddressOpen(decision.OpenAddresses, address) {
		result.Blocked, result.Mode, result.Reason = true, ModeLabel(decision.Mode), decision.Reason
		b.recordSite(host, *decision)
	}
	return result
}

func (b *Blocker) recordSite(site string, decision policy.Decision) {
	now := time.Now()
	key := fmt.Sprintf("site:%s:%d", site, now.Unix()/60)
	b.mu.Lock()
	host, seen := b.host, b.lastSites[key]
	if host != nil && !seen {
		if len(b.lastSites) > 100 {
			b.lastSites = map[string]bool{}
		}
		b.lastSites[key] = true
	}
	b.mu.Unlock()
	if host == nil || seen {
		return
	}
	payload, _ := json.Marshal(map[string]any{"site": site, "mode": decision.Mode, "reason": decision.Reason, "policy_version": decision.PolicyVersion})
	observation := model.Observation{
		SourceInstanceKey: "telemetry-desktop:" + host.DeviceID + ":policy", SourceKind: "desktop_policy", DeviceID: host.DeviceID,
		SourceRecordKey: key, Kind: "desktop.site_block", OccurredAt: now.UTC(), CollectedAt: now.UTC(),
		SourceSchemaVersion: "desktop-policy-1", OriginalPayload: payload,
	}
	if observation.Normalize() == nil {
		host.Queue.Enqueue(context.Background(), observation, now)
	}
}

// BrowserStatus describes the decision in force for the extension's popup,
// and when distractions are next blocked.
func (b *Blocker) BrowserStatus() awserver.BrowserStatus {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	status := awserver.BrowserStatus{BlockingSites: []string{}}
	if !b.fetchedAt.IsZero() {
		fetched := b.fetchedAt.UTC()
		status.FetchedAt = &fetched
	}
	decision := DecisionAt(b.timeline, now)
	if decision == nil {
		return status
	}
	status.Connected = true
	status.Mode, status.Reason, status.BlockAll = ModeLabel(decision.Mode), decision.Reason, decision.BlockAll
	status.BlockingSites = append(status.BlockingSites, decision.Blocked.Domains...)
	for _, segment := range b.timeline {
		if segment.From.After(now) && (segment.Decision.BlockAll || len(segment.Decision.Blocked.Domains) > 0) {
			at := segment.From.UTC()
			status.NextBlockAt, status.NextBlockMode = &at, ModeLabel(segment.Decision.Mode)
			break
		}
	}
	return status
}

// BlocksSite applies a decision to a website: a domain entry covers its
// subdomains, and essentials win.
func BlocksSite(decision policy.Decision, host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	matches := func(domains []string) bool {
		for _, domain := range domains {
			domain = strings.TrimPrefix(strings.ToLower(domain), "www.")
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		}
		return false
	}
	if matches(decision.AlwaysAllowed.Domains) {
		return false
	}
	return decision.BlockAll || matches(decision.Blocked.Domains)
}

// DecisionAt finds the decision in force at now. A device clock slightly
// behind the server still uses the first segment; anything outside the
// timeline returns nil, which blocks nothing.
func DecisionAt(timeline []policy.Segment, now time.Time) *policy.Decision {
	for i := range timeline {
		if !now.Before(timeline[i].From) && now.Before(timeline[i].To) {
			return &timeline[i].Decision
		}
	}
	if len(timeline) > 0 && now.Before(timeline[0].From) && !now.Before(timeline[0].From.Add(-clockTolerance)) {
		return &timeline[0].Decision
	}
	return nil
}

// Blocks reports whether decision blocks the focused Mac app. Essentials and
// system apps are never blocked.
func Blocks(decision policy.Decision, window desktop.Window) bool {
	matches := func(names []string) bool {
		for _, name := range names {
			if strings.EqualFold(name, window.App) || (window.BundleID != "" && strings.EqualFold(name, window.BundleID)) {
				return true
			}
		}
		return false
	}
	if macSystemSafe[window.BundleID] || matches(decision.AlwaysAllowed.MacApps) {
		return false
	}
	return decision.BlockAll || matches(decision.Blocked.MacApps)
}

// ModeLabel names a decision mode for the block screen.
func ModeLabel(mode string) string {
	switch mode {
	case policy.ModeWork:
		return "Work time"
	case policy.ModeBreak:
		return "Break"
	case policy.ModePlanGate:
		return "Plan your day"
	case policy.ModeGate:
		return "Not yet"
	case policy.ModeUrgent:
		return "Urgent task"
	case policy.ModeRecovery:
		return "Recovery"
	case policy.ModeEmergency:
		return "Emergency"
	case policy.ModeClosed:
		return "Focus hours"
	case policy.ModeLeisure:
		return "Free time"
	}
	return mode
}
