//go:build darwin

package desktop

/*
#include <libproc.h>
#include <stdlib.h>

static int td_pid_path(int pid, char *buffer, int size) {
	return proc_pidpath(pid, buffer, (uint32_t)size);
}
*/
import "C"

import (
	"unsafe"

	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

var bundleIDs sync.Map

// AppOfConnection finds the app whose process holds the other end of a
// loopback connection to this process, as the bundle ID of the app that
// contains its executable: a browser's helper processes live inside the
// browser's own app bundle.
func AppOfConnection(remote string) (string, error) {
	_, port, err := net.SplitHostPort(remote)
	if err != nil {
		return "", err
	}
	out, err := exec.Command("/usr/sbin/lsof", "-nP", "-a", "-iTCP@127.0.0.1:"+port, "-sTCP:ESTABLISHED", "-Fp").Output()
	if err != nil {
		return "", fmt.Errorf("lsof: %w", err)
	}
	self := os.Getpid()
	for _, line := range strings.Split(string(out), "\n") {
		pid, err := strconv.Atoi(strings.TrimPrefix(line, "p"))
		if !strings.HasPrefix(line, "p") || err != nil || pid == self {
			continue
		}
		path := pidPath(pid)
		if path == "" {
			continue
		}
		app, _, found := strings.Cut(path, ".app/")
		if !found {
			continue
		}
		app += ".app"
		if id, ok := bundleIDs.Load(app); ok {
			return id.(string), nil
		}
		id, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", app+"/Contents/Info.plist").Output()
		if err != nil {
			return "", fmt.Errorf("bundle id of %s: %w", app, err)
		}
		bundleIDs.Store(app, strings.TrimSpace(string(id)))
		return strings.TrimSpace(string(id)), nil
	}
	return "", fmt.Errorf("no app holds the connection from port %s", port)
}

// pidPath is the executable of a process, or empty when it cannot be read.
func pidPath(pid int) string {
	buffer := (*C.char)(C.malloc(C.PROC_PIDPATHINFO_MAXSIZE))
	defer C.free(unsafe.Pointer(buffer))
	if C.td_pid_path(C.int(pid), buffer, C.PROC_PIDPATHINFO_MAXSIZE) <= 0 {
		return ""
	}
	return C.GoString(buffer)
}
