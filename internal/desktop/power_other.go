//go:build !darwin

package desktop

import "errors"

// WatchPower is not implemented here yet; on Linux it will follow logind's
// PrepareForSleep signal.
func WatchPower(func(sleeping bool)) error {
	return errors.New("power notifications are not implemented on this platform yet")
}
