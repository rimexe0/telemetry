//go:build !darwin

package desktop

// RunMain runs work directly; there is no block screen on this system yet.
func RunMain(work func() error) error { return work() }

// BlockApp is not implemented here yet; Linux app blocking will follow the
// window module.
func BlockApp(title, reason string, pid int) {}

func HideBlock() {}

// BlockScreenSupported reports whether BlockApp does anything here.
func BlockScreenSupported() bool { return false }
