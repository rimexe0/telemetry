package collector

import (
	"context"
	"runtime"
	"time"

	"github.com/rimexe0/telemetry/internal/desktop"
)

const grayscaleCursor = "blocker:grayscale_owned"

// grayscale turns the screen grayscale while the decision asks for it (the
// wind-down before bedtime) and back afterwards. A filter the owner turned
// on themselves is left alone: the blocker only turns off what it turned on,
// and remembers that across restarts.
func (b *Blocker) grayscale(ctx context.Context, host Host, now time.Time) {
	get, set := b.GetGrayscale, b.SetGrayscale
	// Only the Mac has a grayscale switch so far; on Linux agents share the
	// screen, so it is left alone there.
	if get == nil && runtime.GOOS != "darwin" {
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
	if !b.grayscaleLoaded {
		b.grayscaleLoaded = true
		if host.Queue != nil {
			if value, ok, err := host.Queue.LoadCursor(ctx, grayscaleCursor); err == nil && ok {
				b.grayscaleOwned = value == "1"
			}
		}
	}
	owned := b.grayscaleOwned
	b.mu.Unlock()
	wanted := decision != nil && decision.Grayscale
	if wanted == owned {
		return
	}
	on, err := get()
	if err != nil {
		b.health.Failed(now, err)
		return
	}
	switch {
	case wanted && on:
		// The owner already has it on; nothing to do and nothing to undo.
		return
	case wanted:
		err = set(true)
	case on:
		err = set(false)
	}
	if err != nil {
		b.health.Failed(now, err)
		return
	}
	b.mu.Lock()
	b.grayscaleOwned = wanted
	b.mu.Unlock()
	if host.Queue != nil {
		value := "0"
		if wanted {
			value = "1"
		}
		_ = host.Queue.SaveCursor(ctx, grayscaleCursor, value, now)
	}
}
