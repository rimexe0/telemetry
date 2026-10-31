//go:build !darwin

package desktop

import "errors"

func Grayscale() (bool, error) { return false, errors.New("grayscale is only implemented on macOS") }

func SetGrayscale(bool) error { return errors.New("grayscale is only implemented on macOS") }
