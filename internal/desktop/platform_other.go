//go:build !darwin

package desktop

import (
	"errors"
	"runtime"
)

// NewPlatform reports that this system has no sampling layer yet. Linux
// (Hyprland IPC, KWin, Wayland idle-notify) is planned; the rest of the
// collector already builds and runs there.
func NewPlatform() (Platform, error) {
	return nil, errors.New("window and idle sampling is not implemented for " + runtime.GOOS + " yet")
}
