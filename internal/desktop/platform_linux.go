//go:build linux

package desktop

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// On Linux the owner's presence is read from physical input devices only
// (owner request 2026-10-26): agents on the same desktop type and click
// through virtual devices (uinput, as ydotool does) or through the
// compositor, and neither counts as the owner. Only the time of each event is
// kept; which key or button it was is never read past the event type.
//
// Reading /dev/input needs the "input" group. The focused window is not read
// on Linux yet, so window sampling reports nothing.

const (
	evKey = 1
	evRel = 2
	evAbs = 3
	// struct input_event on 64-bit Linux: timeval (16), type, code, value.
	inputEventSize = 24
)

type linux struct {
	mu        sync.Mutex
	lastInput time.Time
	open      map[string]bool
	readable  int
	openError error
}

// NewPlatform returns the Linux implementation and starts watching the
// physical input devices, rescanning for ones plugged in later.
func NewPlatform() (Platform, error) {
	platform := &linux{lastInput: time.Now(), open: map[string]bool{}}
	platform.scan()
	go func() {
		for range time.Tick(30 * time.Second) {
			platform.scan()
		}
	}()
	return platform, nil
}

// physicalInputs lists the event devices that are real keyboards, mice,
// tablets or touch screens: not under /devices/virtual, and able to send
// keys, movement or positions. Sound jacks and lid switches only send
// switch events and are left out.
func physicalInputs() ([]string, error) {
	entries, err := filepath.Glob("/sys/class/input/event*")
	if err != nil {
		return nil, err
	}
	devices := []string{}
	for _, entry := range entries {
		resolved, err := filepath.EvalSymlinks(entry)
		if err != nil || strings.Contains(resolved, "/devices/virtual/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(entry, "device", "capabilities", "ev"))
		if err != nil {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 16, 64)
		if err != nil || mask&(1<<evKey|1<<evRel|1<<evAbs) == 0 {
			continue
		}
		devices = append(devices, "/dev/input/"+filepath.Base(entry))
	}
	return devices, nil
}

func (l *linux) scan() {
	devices, err := physicalInputs()
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.openError = err
		return
	}
	for _, path := range devices {
		if l.open[path] {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			l.openError = err
			continue
		}
		l.open[path] = true
		l.readable++
		go l.read(path, file)
	}
}

func (l *linux) read(path string, file *os.File) {
	defer file.Close()
	buffer := make([]byte, inputEventSize*64)
	for {
		n, err := file.Read(buffer)
		if err != nil {
			// Unplugged: forget it so a rescan opens it again when it returns.
			l.mu.Lock()
			delete(l.open, path)
			l.readable--
			l.mu.Unlock()
			return
		}
		for offset := 0; offset+inputEventSize <= n; offset += inputEventSize {
			switch binary.LittleEndian.Uint16(buffer[offset+16:]) {
			case evKey, evRel, evAbs:
				l.mu.Lock()
				l.lastInput = time.Now()
				l.mu.Unlock()
			}
		}
	}
}

func (*linux) ActiveWindow() (Window, bool, error) { return Window{}, false, nil }

// IdleTime is the time since the last input from a physical device.
func (l *linux) IdleTime() (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.readable == 0 {
		if l.openError != nil {
			return 0, fmt.Errorf("no physical input device is readable (add this user to the input group): %w", l.openError)
		}
		return 0, errors.New("no physical input device found")
	}
	return time.Since(l.lastInput), nil
}

// ScreenLocked counts the desktop as locked when no screen is connected, so
// a machine running headless for agents is never the owner at the desk.
func (*linux) ScreenLocked() (bool, error) {
	if !screenConnected() {
		return true, nil
	}
	session, err := exec.Command("loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Display", "--value").Output()
	if err != nil || strings.TrimSpace(string(session)) == "" {
		// No graphical session: nobody is at this desktop.
		return true, nil
	}
	hint, err := exec.Command("loginctl", "show-session", strings.TrimSpace(string(session)), "-p", "LockedHint", "--value").Output()
	if err != nil {
		return false, fmt.Errorf("loginctl: %w", err)
	}
	return strings.TrimSpace(string(hint)) == "yes", nil
}

func screenConnected() bool {
	statuses, _ := filepath.Glob("/sys/class/drm/card*-*/status")
	for _, path := range statuses {
		if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) == "connected" {
			return true
		}
	}
	return false
}

func (*linux) CanReadTitles(bool) bool { return false }

// Diagnose lists the physical input devices and whether each is readable.
func Diagnose() string {
	devices, err := physicalInputs()
	if err != nil {
		return "input devices: " + err.Error()
	}
	lines := []string{fmt.Sprintf("screen connected: %v", screenConnected())}
	for _, path := range devices {
		name, _ := os.ReadFile("/sys/class/input/" + filepath.Base(path) + "/device/name")
		state := "readable"
		if file, err := os.Open(path); err != nil {
			state = err.Error()
		} else {
			file.Close()
		}
		lines = append(lines, fmt.Sprintf("%s %q: %s", path, strings.TrimSpace(string(name)), state))
	}
	return strings.Join(lines, "\n")
}
