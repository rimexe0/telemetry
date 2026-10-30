//go:build !darwin && !linux

package desktop

import "errors"

func LockScreen() error { return errors.New("locking the screen is not implemented on this platform") }
