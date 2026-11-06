//go:build linux

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// On KDE Plasma only KWin knows which window is in front, on Wayland as on
// X11. A small KWin script tells this process of each change over the
// session bus; short one-off scripts minimise a blocked app's windows. Other
// desktops are not read yet.

const (
	kwinService     = "org.kde.KWin"
	collectorBus    = "cyou.rime.telemetry.Collector"
	windowPath      = "/cyou/rime/telemetry/Window"
	windowInterface = "cyou.rime.telemetry.Window"
	watchScriptName = "telemetry-window-watch"
)

// watchScript reports the window in front when it changes and when its
// title does. The process ID goes as text, which every KWin version sends
// the same way.
const watchScript = `
function send(w) {
  if (!w) {
    callDBus("` + collectorBus + `", "` + windowPath + `", "` + windowInterface + `", "None");
    return;
  }
  callDBus("` + collectorBus + `", "` + windowPath + `", "` + windowInterface + `", "Active",
    w.caption || "", w.resourceClass || "", w.desktopFileName || "", String(w.pid));
}
function hook(w) {
  w.captionChanged.connect(function () { if (workspace.activeWindow === w) send(w); });
}
workspace.windowList().forEach(hook);
workspace.windowAdded.connect(hook);
workspace.windowActivated.connect(send);
send(workspace.activeWindow);
`

type kwin struct {
	conn *dbus.Conn
	dir  string

	mu     sync.Mutex
	window Window
	ok     bool
	pids   map[int]string // window process → app ID
	notice uint32         // the notice shown last, replaced by the next
	hides  int
}

var (
	plasma     *kwin
	plasmaErr  error
	plasmaOnce sync.Once
)

// desktopKWin connects to KWin once per process. It returns nil when this
// session has no KWin, and an error when it has one that could not be used.
func desktopKWin() (*kwin, error) {
	plasmaOnce.Do(func() { plasma, plasmaErr = startKWin() })
	return plasma, plasmaErr
}

func startKWin() (*kwin, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil
	}
	var running bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, kwinService).Store(&running); err != nil || !running {
		conn.Close()
		return nil, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	k := &kwin{conn: conn, dir: filepath.Join(cache, "telemetry", "kwin"), pids: map[int]string{}}
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return nil, err
	}
	if err := conn.Export(windowReceiver{k}, windowPath, windowInterface); err != nil {
		return nil, err
	}
	reply, err := conn.RequestName(collectorBus, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("KWin: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, errors.New("KWin: another collector already receives window changes")
	}
	if err := k.loadWatch(); err != nil {
		return nil, err
	}
	// KWin forgets the script when it restarts.
	go func() {
		for range time.Tick(30 * time.Second) {
			var loaded bool
			if k.scripting().Call("org.kde.kwin.Scripting.isScriptLoaded", 0, watchScriptName).Store(&loaded) == nil && !loaded {
				k.loadWatch()
			}
		}
	}()
	return k, nil
}

func (k *kwin) scripting() dbus.BusObject { return k.conn.Object(kwinService, "/Scripting") }

// runScript loads source as a KWin script under name and starts it.
func (k *kwin) runScript(name, source string) error {
	path := filepath.Join(k.dir, name+".js")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		return err
	}
	k.scripting().Call("org.kde.kwin.Scripting.unloadScript", 0, name)
	var id int32
	if err := k.scripting().Call("org.kde.kwin.Scripting.loadScript", 0, path, name).Store(&id); err != nil {
		return fmt.Errorf("KWin: load script: %w", err)
	}
	if id < 0 {
		return errors.New("KWin: the script did not load")
	}
	if call := k.conn.Object(kwinService, dbus.ObjectPath("/Scripting/Script"+strconv.Itoa(int(id)))).Call("org.kde.kwin.Script.run", 0); call.Err != nil {
		return fmt.Errorf("KWin: run script: %w", call.Err)
	}
	return nil
}

func (k *kwin) loadWatch() error { return k.runScript(watchScriptName, watchScript) }

// windowReceiver takes the watch script's calls.
type windowReceiver struct{ k *kwin }

func (r windowReceiver) Active(caption, class, desktopFile, pid string) *dbus.Error {
	id := desktopFile
	if id == "" {
		id = class
	}
	number, _ := strconv.Atoi(pid)
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	r.k.window = Window{App: appName(id), BundleID: id, Title: caption, PID: number}
	r.k.ok = id != ""
	if number > 0 && id != "" {
		if len(r.k.pids) > 200 {
			r.k.pids = map[int]string{}
		}
		r.k.pids[number] = id
	}
	return nil
}

func (r windowReceiver) None() *dbus.Error {
	r.k.mu.Lock()
	r.k.ok = false
	r.k.mu.Unlock()
	return nil
}

// appName makes a readable name from a desktop file name or window class:
// "org.kde.konsole" becomes "Konsole", "helium" becomes "Helium".
func appName(id string) string {
	name := id[strings.LastIndex(id, ".")+1:]
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func (k *kwin) active() (Window, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.window, k.ok
}

// appOfProcess finds the app of pid or of the nearest process above it that
// showed a window: a browser's network process runs under the browser.
func (k *kwin) appOfProcess(pid int) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for step := 0; pid > 1 && step < 16; step++ {
		if id, ok := k.pids[pid]; ok {
			return id, true
		}
		pid = parentProcess(pid)
	}
	return "", false
}

// processOfApp names a process of an app that showed a window.
func (k *kwin) processOfApp(id string) (int, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for pid, app := range k.pids {
		if app == id {
			return pid, true
		}
	}
	return 0, false
}

func parentProcess(pid int) int {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The command name is in parentheses and may hold spaces.
	fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
	if len(fields) < 2 {
		return 0
	}
	parent, _ := strconv.Atoi(fields[1])
	return parent
}

// hide minimises every window of pid and shows title and reason in a notice
// that disappears by itself.
func (k *kwin) hide(title, reason string, pid int) {
	k.mu.Lock()
	k.hides++
	name := fmt.Sprintf("telemetry-hide-%d", k.hides%4)
	k.mu.Unlock()
	if pid > 0 {
		source := fmt.Sprintf("for (const w of workspace.windowList()) if (w.pid === %d) w.minimized = true;", pid)
		if k.runScript(name, source) == nil {
			go func() {
				time.Sleep(3 * time.Second)
				k.scripting().Call("org.kde.kwin.Scripting.unloadScript", 0, name)
			}()
		}
	}
	k.mu.Lock()
	replaces := k.notice
	k.mu.Unlock()
	var id uint32
	err := k.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").Call(
		"org.freedesktop.Notifications.Notify", 0, "Telemetry", replaces, "", title, reason, []string{},
		map[string]dbus.Variant{}, int32(8000)).Store(&id)
	if err == nil {
		k.mu.Lock()
		k.notice = id
		k.mu.Unlock()
	}
}
