//go:build darwin

package desktop

/*
#include <dlfcn.h>

// SACLockScreenImmediate in the private login framework locks the session
// the way Control-Command-Q does: nothing is closed.
static int td_lock_screen(void) {
	void *login = dlopen("/System/Library/PrivateFrameworks/login.framework/Versions/Current/login", RTLD_LAZY);
	if (!login) return -1;
	int (*lock)(void) = (int (*)(void))dlsym(login, "SACLockScreenImmediate");
	if (!lock) return -2;
	return lock();
}
*/
import "C"

import (
	"fmt"
	"os/exec"
)

// LockScreen locks the session without closing anything.
func LockScreen() error {
	if result := C.td_lock_screen(); result >= 0 {
		return nil
	} else if out, err := exec.Command("/usr/bin/pmset", "displaysleepnow").CombinedOutput(); err != nil {
		return fmt.Errorf("lock screen (%d): %v: %s", int(result), err, out)
	}
	return nil
}
