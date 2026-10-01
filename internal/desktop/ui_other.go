//go:build !darwin

package desktop

// RunMain runs work directly; there is no block screen on this system yet.
func RunMain(work func() error) error { return work() }

// ShowBlock is not implemented here yet; a Linux block screen will follow
// the window module.
func ShowBlock(mode, reason string, pid int) {}

func HideBlock() {}

// BlockScreenSupported reports whether ShowBlock does anything here.
func BlockScreenSupported() bool { return false }
