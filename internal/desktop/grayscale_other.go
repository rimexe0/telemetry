//go:build !darwin && !linux

package desktop

import "errors"

func Grayscale() (bool, error) { return false, ErrGrayscaleUnknown }

func SetGrayscale(bool) error { return errors.New("grayscale is not implemented on this system") }

// GrayscaleSupported reports whether this system has a grayscale filter to
// follow.
func GrayscaleSupported() bool { return false }
