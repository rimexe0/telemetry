//go:build !darwin && !linux

package desktop

import (
	"errors"
	"runtime"
)

// ReadsWindows reports whether this system's collector records the focused
// window and its title.
const ReadsWindows = false

// NewPlatform reports that this system has no sampling layer yet. Linux
// (Hyprland IPC, KWin, Wayland idle-notify) is planned; the rest of the
// collector already builds and runs there.
func NewPlatform() (Platform, error) {
	return nil, errors.New("window and idle sampling is not implemented for " + runtime.GOOS + " yet")
}

// Diagnose has nothing to report on systems without a sampling layer.
func Diagnose() string { return "no sampling layer on " + runtime.GOOS }
