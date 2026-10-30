//go:build linux

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// LockScreen locks the graphical session through logind without closing
// anything.
func LockScreen() error {
	session, err := exec.Command("loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Display", "--value").Output()
	if err != nil || strings.TrimSpace(string(session)) == "" {
		return fmt.Errorf("no graphical session to lock")
	}
	if out, err := exec.Command("loginctl", "lock-session", strings.TrimSpace(string(session))).CombinedOutput(); err != nil {
		return fmt.Errorf("loginctl lock-session: %v: %s", err, out)
	}
	return nil
}
