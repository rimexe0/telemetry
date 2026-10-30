//go:build !darwin

package desktop

import "errors"

// AppOfConnection is only implemented on macOS.
func AppOfConnection(string) (string, error) {
	return "", errors.New("finding the app of a connection is only implemented on macOS")
}
