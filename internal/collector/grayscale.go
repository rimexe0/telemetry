package collector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
)

const (
	grayscaleCursor = "blocker:grayscale_owned"
	// grayscaleWindDownCursor keeps the owner's choice for this wind-down
	// and a report the server has not taken yet, across restarts.
	grayscaleWindDownCursor = "blocker:grayscale_wind_down"
	// grayscaleNoticeFor is how long the notice stays; unanswered, the
	// wind-down keeps grayscale on.
	grayscaleNoticeFor = 2 * time.Minute
	// grayscaleBusyRetry waits for another notice to go before this one.
	grayscaleBusyRetry = 15 * time.Second
	// grayscaleReportRetry spaces reports the server did not take.
	grayscaleReportRetry = 30 * time.Second
)

// grayscaleState is guarded by the blocker's mutex.
type grayscaleState struct {
	loaded bool
	// owned is true while the blocker has the filter on; it only turns off
	// what it turned on.
	owned bool
	// declined is the owner's "Keep it off" for this wind-down.
	declined bool
	asking   bool
	askAfter time.Time
	// pending is a decline or restore the server has not taken yet.
	pending     string
	reporting   bool
	reportAfter time.Time
	// screen is on, off or unknown as last read, for the status.
	screen string
}

type grayscaleWindDown struct {
	Declined bool   `json:"declined"`
	Pending  string `json:"pending,omitempty"`
}

// grayscale turns the screen grayscale while the decision asks for it (the
// wind-down before bedtime) and back afterwards. A filter the owner turned
// on themselves is left alone: the blocker only turns off what it turned
// on, and remembers that across restarts.
//
// When the owner switches it off during the wind-down, a small notice asks
// once instead of switching it back silently (2026-11-26): keep it off,
// which costs the budget's surcharge until grayscale is no longer wanted,
// or switch it back. Turning it on again later ends the surcharge. Both are
// reported to the server, which charges for them.
func (b *Blocker) grayscale(ctx context.Context, host Host, now time.Time) {
	get, set := b.GetGrayscale, b.SetGrayscale
	if get == nil && !desktop.GrayscaleSupported() {
		return
	}
	if get == nil {
		get = desktop.Grayscale
	}
	if set == nil {
		set = desktop.SetGrayscale
	}
	b.mu.Lock()
	decision := DecisionAt(b.timeline, now)
	b.loadGrayscale(ctx, host)
	wanted := decision != nil && decision.Grayscale
	if !wanted && b.gray.declined {
		// The wind-down is over, and with it the choice; the server ends
		// the surcharge once grayscale is no longer wanted.
		b.gray.declined = false
		b.saveGrayscale(ctx, host, now)
	}
	owned := b.gray.owned
	b.mu.Unlock()
	b.reportGrayscale(ctx, host, now)
	if !wanted && !owned {
		return
	}
	on, err := get()
	if errors.Is(err, desktop.ErrGrayscaleUnknown) {
		// Not readable here: say so, and do nothing on a guess.
		b.mu.Lock()
		b.gray.screen = "unknown"
		b.mu.Unlock()
		return
	}
	if err != nil {
		b.health.Failed(now, err)
		return
	}
	b.mu.Lock()
	b.gray.screen = map[bool]string{true: "on", false: "off"}[on]
	turn, ask := 0, false // turn: 1 on, -1 off
	switch {
	case !wanted:
		if on {
			turn = -1
		}
	case on:
		// Gray already: by the blocker, by the owner's own filter, which it
		// then never turns off, or turned on again after "Keep it off".
		if b.gray.declined {
			b.gray.declined = false
			b.queueGrayscaleReport("restore")
			b.saveGrayscale(ctx, host, now)
		}
	case !owned:
		// The wind-down starts: turn it on.
		turn = 1
	case b.gray.declined || b.gray.asking || now.Before(b.gray.askAfter):
	default:
		// The owner switched it off: ask once.
		b.gray.asking, ask = true, true
	}
	b.mu.Unlock()
	if ask {
		b.run(func() { b.askGrayscale(ctx, host, set) })
		return
	}
	if turn == 0 && wanted {
		return
	}
	if turn != 0 {
		if err := set(turn > 0); err != nil {
			b.health.Failed(now, err)
			return
		}
	}
	b.mu.Lock()
	b.gray.owned = wanted
	b.saveGrayscale(ctx, host, now)
	b.mu.Unlock()
}

// askGrayscale shows the notice and does what the owner chose.
func (b *Blocker) askGrayscale(ctx context.Context, host Host, set func(bool) error) {
	prompt := b.Prompt
	if prompt == nil {
		if desktop.PromptShowing() {
			b.mu.Lock()
			b.gray.asking, b.gray.askAfter = false, time.Now().Add(grayscaleBusyRetry)
			b.mu.Unlock()
			return
		}
		prompt = desktop.Prompt
	}
	b.mu.Lock()
	keep := keepGrayscaleOff(b.budget)
	b.mu.Unlock()
	choice := prompt("Grayscale is off", "It is on for the wind-down before bedtime.", []string{keep, "Switch back"}, grayscaleNoticeFor)
	now := time.Now()
	b.mu.Lock()
	b.gray.asking = false
	decision := DecisionAt(b.timeline, now)
	wanted := decision != nil && decision.Grayscale
	if wanted && choice == 0 {
		// Sent with the next sample.
		b.gray.declined = true
		b.queueGrayscaleReport("decline")
		b.saveGrayscale(ctx, host, now)
	}
	b.mu.Unlock()
	if !wanted || choice == 0 {
		return
	}
	// "Switch back", or no answer: the wind-down keeps it on.
	if err := set(true); err != nil {
		b.health.Failed(now, err)
	}
}

// keepGrayscaleOff words the notice's first button with the surcharge the
// budget names.
func keepGrayscaleOff(budget budgetState) string {
	if !budget.Enabled {
		return "Keep it off until bedtime ends"
	}
	if surcharge, ok := budget.grayscaleSurcharge(); ok {
		return "Keep it off: +" + strconv.FormatFloat(surcharge, 'f', -1, 64) + "× until bedtime ends"
	}
	return "Keep it off: distraction time costs more until bedtime ends"
}

// queueGrayscaleReport keeps a report for the server. A restore of a decline
// the server never heard cancels it. Called with b.mu held.
func (b *Blocker) queueGrayscaleReport(action string) {
	if b.gray.pending != "" && b.gray.pending != action {
		b.gray.pending = ""
		return
	}
	b.gray.pending, b.gray.reportAfter = action, time.Time{}
}

// reportGrayscale sends the pending report, retrying until the server takes
// it. A refusal (4xx) is not retried. The new answer then shows the rate.
func (b *Blocker) reportGrayscale(ctx context.Context, host Host, now time.Time) {
	b.mu.Lock()
	action := b.gray.pending
	if action == "" || b.gray.reporting || now.Before(b.gray.reportAfter) || b.Credentials == nil {
		b.mu.Unlock()
		return
	}
	b.gray.reporting = true
	b.mu.Unlock()
	b.run(func() {
		body, _ := json.Marshal(map[string]string{"action": action, "platform": grayscalePlatform()})
		status, _, err := b.deviceRequest(ctx, http.MethodPost, "/v1/device/grayscale", body)
		done := err == nil && status >= 200 && status < 500 && status != http.StatusTooManyRequests
		b.mu.Lock()
		b.gray.reporting = false
		if done {
			if b.gray.pending == action {
				b.gray.pending = ""
			}
			b.saveGrayscale(ctx, host, time.Now())
		} else {
			b.gray.reportAfter = time.Now().Add(grayscaleReportRetry)
		}
		b.mu.Unlock()
		if done && status < 300 {
			b.refresh(ctx, host)
		}
	})
}

func grayscalePlatform() string {
	if runtime.GOOS == "darwin" {
		return "mac"
	}
	return runtime.GOOS
}

// loadGrayscale reads the saved state once. Called with b.mu held.
func (b *Blocker) loadGrayscale(ctx context.Context, host Host) {
	if b.gray.loaded {
		return
	}
	b.gray.loaded = true
	if host.Queue == nil {
		return
	}
	if value, ok, err := host.Queue.LoadCursor(ctx, grayscaleCursor); err == nil && ok {
		b.gray.owned = value == "1"
	}
	if value, ok, err := host.Queue.LoadCursor(ctx, grayscaleWindDownCursor); err == nil && ok {
		var saved grayscaleWindDown
		if json.Unmarshal([]byte(value), &saved) == nil {
			b.gray.declined, b.gray.pending = saved.Declined, saved.Pending
		}
	}
}

// saveGrayscale keeps the state across restarts. Called with b.mu held.
func (b *Blocker) saveGrayscale(ctx context.Context, host Host, now time.Time) {
	if host.Queue == nil {
		return
	}
	owned := "0"
	if b.gray.owned {
		owned = "1"
	}
	_ = host.Queue.SaveCursor(ctx, grayscaleCursor, owned, now)
	windDown, _ := json.Marshal(grayscaleWindDown{Declined: b.gray.declined, Pending: b.gray.pending})
	_ = host.Queue.SaveCursor(ctx, grayscaleWindDownCursor, string(windDown), now)
}

// run starts work beside the sampling loop.
func (b *Blocker) run(work func()) {
	if b.async != nil {
		b.async(work)
		return
	}
	go work()
}
