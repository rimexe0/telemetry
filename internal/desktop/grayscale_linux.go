//go:build linux

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/godbus/dbus/v5"
)

// On KDE Plasma the screen turns grayscale through KWin's colour blindness
// correction effect in its "All (grayscale mode)" mode, the switch in System
// Settings → Accessibility → Color Blindness Correction (Plasma 6.3 and
// later). KWin says over D-Bus whether the effect runs; kwinrc says which
// mode it runs in. The filter is switched the way System Settings does it:
// the setting is saved, so it survives a KWin restart and the settings page
// shows it, and KWin loads or unloads the effect at once.
const (
	colourFilterEffect = "colorblindnesscorrection"
	colourFilterGroup  = "Effect-colorblindnesscorrection"
	colourFilterKey    = "colorblindnesscorrectionEnabled"
	// grayscaleMode is the effect's Mode for "All (grayscale mode)".
	grayscaleMode = "3"
)

func kwinEffects() (dbus.BusObject, error) {
	conn, err := sessionBus()
	if err != nil {
		return nil, err
	}
	return conn.Object(kwinService, "/Effects"), nil
}

// GrayscaleSupported reports whether this system has a grayscale filter to
// follow. Where KWin cannot be asked, Grayscale says the state is unknown.
func GrayscaleSupported() bool { return true }

// Grayscale reports whether KWin shows the screen in grayscale. Without
// KWin, or with a KWin that has no colour filter, the state is unknown.
func Grayscale() (bool, error) {
	effects, err := kwinEffects()
	if err != nil {
		return false, ErrGrayscaleUnknown
	}
	var loaded bool
	if err := effects.Call("org.kde.kwin.Effects.isEffectLoaded", 0, colourFilterEffect).Store(&loaded); err != nil {
		return false, ErrGrayscaleUnknown
	}
	if !loaded {
		var supported bool
		if err := effects.Call("org.kde.kwin.Effects.isEffectSupported", 0, colourFilterEffect).Store(&supported); err != nil || !supported {
			return false, ErrGrayscaleUnknown
		}
		return false, nil
	}
	mode, err := kwinConfig(colourFilterGroup, "Mode")
	if err != nil {
		return false, ErrGrayscaleUnknown
	}
	// Another mode corrects colours for colour blindness; it is not gray.
	return mode == grayscaleMode, nil
}

// SetGrayscale turns KWin's grayscale filter on or off.
func SetGrayscale(on bool) error {
	effects, err := kwinEffects()
	if err != nil {
		return fmt.Errorf("grayscale: %w", err)
	}
	if on {
		if err := writeKWinConfig(colourFilterGroup, "Mode", grayscaleMode); err != nil {
			return err
		}
	}
	if err := writeKWinConfig("Plugins", colourFilterKey, strconv.FormatBool(on)); err != nil {
		return err
	}
	if !on {
		if call := effects.Call("org.kde.kwin.Effects.unloadEffect", 0, colourFilterEffect); call.Err != nil {
			return fmt.Errorf("grayscale: %w", call.Err)
		}
		return nil
	}
	// Loading does nothing to an effect that runs; reconfiguring makes it
	// take the grayscale mode.
	if call := effects.Call("org.kde.kwin.Effects.loadEffect", 0, colourFilterEffect); call.Err != nil {
		return fmt.Errorf("grayscale: %w", call.Err)
	}
	if call := effects.Call("org.kde.kwin.Effects.reconfigureEffect", 0, colourFilterEffect); call.Err != nil {
		return fmt.Errorf("grayscale: %w", call.Err)
	}
	var loaded bool
	if err := effects.Call("org.kde.kwin.Effects.isEffectLoaded", 0, colourFilterEffect).Store(&loaded); err != nil || !loaded {
		return errors.New("grayscale: KWin did not load its colour filter")
	}
	return nil
}

// kwinConfig reads a kwinrc value: the owner's file first, then the
// system's; a value in neither is KWin's default, which is "0" for Mode.
func kwinConfig(group, key string) (string, error) {
	home, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	for _, path := range []string{filepath.Join(home, "kwinrc"), "/etc/xdg/kwinrc"} {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if value, ok := kconfigValue(string(data), group, key); ok {
			return value, nil
		}
	}
	return "0", nil
}

// writeKWinConfig saves a kwinrc value with KDE's own tool, which keeps the
// file's locking and format.
func writeKWinConfig(group, key, value string) error {
	tool, err := exec.LookPath("kwriteconfig6")
	if err != nil {
		return errors.New("grayscale: kwriteconfig6 is not installed")
	}
	if out, err := exec.Command(tool, "--file", "kwinrc", "--group", group, "--key", key, value).CombinedOutput(); err != nil {
		return fmt.Errorf("grayscale: kwriteconfig6: %v: %s", err, out)
	}
	return nil
}
