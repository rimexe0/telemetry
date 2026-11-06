//go:build !darwin && !linux

package desktop

import "errors"

// RunMain runs work directly; there is no block screen on this system yet.
func RunMain(work func() error) error { return work() }

// BlockApp is not implemented here yet; Linux app blocking will follow the
// window module.
func BlockApp(title, reason string, pid int) {}

func HideBlock() {}

// BlockScreenSupported reports whether BlockApp does anything here.
func BlockScreenSupported() bool { return false }

// OpenInApp is not implemented here yet.
func OpenInApp(id, address string) error {
	return errors.New("opening an address in an app is not implemented here")
}
