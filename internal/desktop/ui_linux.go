//go:build linux

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// RunMain runs work directly; Linux needs no main-thread event loop.
func RunMain(work func() error) error { return work() }

// BlockApp minimises the app pid's windows on KDE Plasma (it keeps running)
// and shows title and reason in a notice that disappears by itself. It does
// nothing on other desktops.
func BlockApp(title, reason string, pid int) {
	if k, _ := desktopKWin(); k != nil {
		k.hide(title, reason, pid)
	}
}

// HideBlock has nothing to remove: the notice disappears by itself.
func HideBlock() {}

// BlockScreenSupported reports whether BlockApp does anything here.
func BlockScreenSupported() bool {
	k, _ := desktopKWin()
	return k != nil
}

// OpenInApp opens address in the app with this ID, which must have shown a
// window: its own program is started again with the address, which hands
// it to the running copy.
func OpenInApp(id, address string) error {
	k, _ := desktopKWin()
	if k == nil {
		return errors.New("opening an address in an app needs KDE Plasma")
	}
	pid, ok := k.processOfApp(id)
	if !ok {
		return errors.New(id + " has shown no window")
	}
	program, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return err
	}
	command := exec.Command(program, address)
	if err := command.Start(); err != nil {
		return err
	}
	go command.Wait()
	return nil
}
