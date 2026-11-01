package collector

import (
	"context"

	"github.com/rimexe0/telemetry/internal/desktop"
)

// Window records the focused app, window title and AFK state through the
// platform layer.
type Window struct {
	health desktop.Health
}

func (*Window) Name() string { return "window" }

func (w *Window) Run(ctx context.Context, host Host) error {
	platform, err := desktop.NewPlatform()
	if err != nil {
		return err
	}
	return desktop.Run(ctx, host.Store, platform, desktop.Options{Hostname: host.Hostname, Health: &w.health, Logf: host.Logf})
}

func (w *Window) Status() map[string]any {
	snapshot := w.health.Snapshot()
	status := map[string]any{"titles_readable": snapshot.TitlesReadable, "reads_windows": desktop.ReadsWindows}
	if snapshot.LastWindowAt != nil {
		status["last_window_sample_at"] = *snapshot.LastWindowAt
	}
	if snapshot.LastError != "" {
		status["last_sampling_error"] = snapshot.LastError
		status["last_sampling_error_at"] = snapshot.LastErrorAt
	}
	return status
}
